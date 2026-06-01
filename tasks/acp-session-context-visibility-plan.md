# ACP Session Context Visibility Plan

## Goal

Make ACP receive the full Substrate session context in its first prompt turn while preserving a clear two-part transcript for users:

1. **Session context** — the long Substrate-provided instructions, work item, repo/sub-plan context, and constraints.
2. **Prompt** — the short user/turn kickoff such as “Begin planning…” or “Begin implementing…”.

ACP has no dedicated system-prompt channel, so its actual `session/prompt` payload must fold both parts together. Other harnesses should keep their existing delivery semantics but expose the hidden session context in logs/UI.

## Current State

### Planning builds two prompt fields

`internal/orchestrator/planning.go` builds:

- `SystemPrompt`: full planning context rendered from `planningPromptTmpl`.
- `UserPrompt`: short kickoff text.

The visible transcript currently shows only the short `UserPrompt`, which hides the meaningful context from the operator.

### Harness behavior differs

- **OMP / Claude Agent SDK**: pass `SystemPrompt` through a real SDK/system-prompt path, then send `UserPrompt` as a user turn.
- **Codex / OpenCode**: already fold `SystemPrompt + UserPrompt` for actual delivery.
- **ACP**: currently sends only `UserPrompt`, dropping `SystemPrompt` entirely.

## Desired Behavior

### ACP delivery

For ACP agent sessions, send exactly one initial ACP prompt turn containing:

```text
<system/session context>

<prompt>
```

Do not send the session context as a separate ACP prompt turn.

If `UserPrompt == ""`, do not auto-send anything. Resumed sessions may intentionally start idle until a compact/message call occurs.

### Transcript visualization

All harnesses should show the two conceptual parts separately when both exist:

```text
Session context
<full session context>

Prompt
<short turn prompt>
```

This is display/logging parity only; delivery semantics can remain harness-specific.

## Implementation Steps

### 1. Add a transcript input kind for session context

Files:

- `internal/sessionlog/parse.go`
- `internal/tui/views/session_transcript.go`
- `internal/tui/views/session_transcript_test.go`

Changes:

- `internal/sessionlog/parse.go` already preserves arbitrary `input_kind` values for canonical events; no production parser change is expected unless tests expose a gap.
- Treat `input_kind: "session_context"` as a normal input entry.
- Render it with a label such as `Session context` or `Session instructions`.
- Keep `prompt`, `message`, and `answer` labels unchanged.
- Add width-bounded rendering coverage for long session-context text.

### 2. Emit session-context log events from bridge harnesses

Files:

- `bridge/omp-bridge.ts`
- `bridge/claude-agent-bridge.ts`

Changes:

- Extend each bridge's `emitInput` TypeScript literal union to include `"session_context"`:
  - OMP currently accepts `"prompt" | "message" | "answer"`.
  - Claude Agent currently accepts `"prompt" | "message" | "steer" | "answer"`.
- After receiving the init message and extracting `system_prompt`, emit:

```json
{"type":"input","input_kind":"session_context","text":"..."}
```

- Keep the existing prompt event unchanged.
- Continue passing `system_prompt` through the native system-prompt channel.

Expected transcript:

1. Session context block.
2. Prompt block.
3. Assistant/tool output.

### 3. Fold ACP system prompt into the actual prompt payload

Files:

- `internal/adapter/acp/harness.go`
- `internal/adapter/acp/session.go`

Changes:

- Store the Substrate session context (`opts.SystemPrompt`) on the ACP session or pass it into the initial prompt helper.
- Replace the current initial call from:

```go
s.startPrompt(opts.UserPrompt)
```

with a helper that folds context and prompt for ACP delivery.

- Use a helper shape like:

```go
func combineSessionContextAndPrompt(context, prompt string) string
```

Rules:

- trim empty sides;
- if both exist, join with `\n\n`;
- if only one exists, return that one;
- but only auto-start when `UserPrompt` is non-empty.

### 4. Log ACP display events separately

Files:

- `internal/adapter/acp/session.go`
- maybe `internal/adapter/acp/harness.go`

Changes:

Before sending the folded ACP prompt, write canonical session-log events for display:

```json
{"type":"event","event":{"type":"input","input_kind":"session_context","text":"..."}}
{"type":"event","event":{"type":"input","input_kind":"prompt","text":"..."}}
```

Implementation notes:

- Add an ACP helper such as `writeCanonicalInputLog(inputKind, text string)` / `writeSyntheticInputLog(...)` that writes raw canonical log lines to the session log file.
- Do **not** rely on `s.emit(...)` for these display events. `s.emit(...)` only sends `adapter.AgentEvent`s to the in-memory event channel; it does not write ACP transcript log lines.
- Do not rely on ACP `user_message_chunk` history echoes; those are agent-dependent and may include the folded payload.

If the session context is empty, write only the prompt event.

### 5. Fold ACP context for later first messages where relevant

ACP has no system-prompt channel for any session kind. For session kinds that start without an automatic `UserPrompt` but receive a first `SendMessage`, the session context still needs delivery.

Files:

- `internal/adapter/acp/session.go`

Changes:

- Track whether session context has already been sent. The flag must be concurrency-safe: protect it with `promptMu` or use an atomic field so an automatic initial prompt and a concurrent first `SendMessage` cannot both send the context.
- Define outbound folding behavior explicitly:
  - automatic initial prompt: fold context once and log entries as `session_context` + `prompt`;
  - `SendMessage`: if context has not been delivered, fold context once and log entries as `session_context` + `message`;
  - later `SendMessage` calls: send and log only the message text;
  - `Steer`: do not consume or resend session context unless an explicit product decision changes steering semantics;
  - `Compact`: do not consume or resend session context.
- On the first user/message prompt sent through ACP, fold the session context into the actual ACP payload.
- Log it as two conceptual entries:
  - session context
  - message/prompt
- Subsequent messages should send only the message text.

This covers foreman/manual-like ACP flows without introducing a separate system turn.

### 6. Add display parity for already-folded harnesses

Files:

- `internal/adapter/codex/harness.go`
- `internal/adapter/opencode/harness.go`

Changes:

- Keep actual delivery unchanged: both already fold `SystemPrompt + UserPrompt`.
- Add synthetic canonical log events so the transcript displays the two parts separately.

Expected result:

- Actual prompt delivery remains one combined string.
- Transcript shows:
  - session context
  - prompt

Implementation notes:

- Codex currently writes raw Codex JSONL lines to its session log. Add a canonical-event writer for synthetic input events; raw Codex JSONL alone is not parsed as transcript input.
- OpenCode already writes canonical `adapter.AgentEvent` records via `writeLogEvent`; use or extend that path for synthetic input entries.

### 7. Filter duplicated ACP history echoes

Files:

- `internal/adapter/acp/session.go`
- `internal/adapter/acp/events.go` or `internal/sessionlog/parse.go` if filtering is better centralized there

Changes:

- ACP agents may emit `user_message_chunk` history for the locally sent folded prompt.
- Canonical synthetic input events must be the source of truth for locally sent prompts.
- Suppress or deduplicate ACP `user_message_chunk` echoes that exactly match locally sent folded prompt payloads, so the transcript does not show:
  1. `Session context`
  2. `Prompt` / `Feedback`
  3. a third generic `Input` block containing the folded context plus prompt.
- Keep unrelated remote/user history visible if it does not match a locally sent prompt.

### 8. Tests

#### ACP tests

File:

- `internal/adapter/acp/harness_test.go`

Add coverage that:

- `session/prompt` receives the folded system+user text in a single prompt request.
- `StartSession` with `SystemPrompt` but empty `UserPrompt` does not auto-send a prompt.
- first `SendMessage` folds the context when it has not yet been delivered.
- second `SendMessage` does not repeat the context.
- `Steer` and `Compact` do not consume or resend the pending session context.
- ACP session logs contain canonical `session_context` and `prompt`/`message` input events before the folded `session/prompt` delivery is logged.
- ACP `user_message_chunk` echoes matching the locally sent folded payload do not render as a duplicate generic `Input` block.

#### Transcript tests

File:

- `internal/tui/views/session_transcript_test.go`

Add coverage that:

- `input_kind=session_context` groups as a prompt/input block.
- rendered label is `Session context` / `Session instructions`.
- rendered output stays within narrow terminal width.

#### Session log parser tests

File:

- `internal/sessionlog/parse_test.go`

Add coverage that:

- canonical event with `input_kind=session_context` parses as `KindInput` with `InputKind == "session_context"`.
- if ACP history echo filtering is implemented in the parser, only matching locally sent folded prompt echoes are suppressed; unrelated `user_message_chunk` history remains visible.

#### Bridge tests

Files:

- `internal/adapter/bridge/bridge_test.go`
- `internal/adapter/claudeagent/harness_test.go` or bridge-level tests if present

Add/adjust coverage only where cheap and deterministic:

- bridge event mapping preserves `input_kind=session_context`.

#### Codex/OpenCode tests

Files:

- `internal/adapter/codex/harness_test.go`
- `internal/adapter/opencode/*_test.go`

Add coverage that logs contain separate session-context and prompt entries while actual delivery remains folded.

## Risks

- Large session-context log lines may hit scanner limits. Current scanner buffer is 10 MB, which should be enough for existing prompts; add a realistic large-context test if prompt sizes grow.
- If session context contains sensitive data, this makes it visible in logs/UI. This is intentional per the request, but should be treated as operator-visible transcript content.
- Synthetic log event ordering matters. Write `session_context` and `prompt`/`message` entries before the folded ACP `session/prompt` frame so transcript display is stable even if the agent immediately streams output.

## Verification

Run focused tests only:

```sh
go test ./internal/adapter/acp ./internal/sessionlog ./internal/tui/views
```

If Codex/OpenCode logging is changed:

```sh
go test ./internal/adapter/codex ./internal/adapter/opencode
```

If bridge TypeScript changes are made:

```sh
bun --cwd bridge run typecheck
```
