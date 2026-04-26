# Stage-Aware Agent Question Routing

## Purpose

Substrate agents can ask questions through multiple mechanisms. Historically, the explicit `ask_foreman` tool was treated as the main implementation-agent escalation path, but planning agents can also see that tool even though there is no Foreman running during planning. Harnesses also have their own native/default question mechanisms, such as OMP's `ask` tool, Claude Code's ask-user-question tool, and OpenCode's `question.asked` event.

This document defines the product behavior and implementation plan for routing all agent question mechanisms consistently.

## Product definition

Substrate should treat every agent question mechanism as one normalized product event:

```text
AgentQuestionAsked
```

The source of the question does not determine the routing behavior. The current session stage determines routing behavior.

```text
Planning stage
  -> route directly to the human
  -> deliver the answer back to the live planning harness
  -> do not store the question/answer as Foreman FAQ
  -> do not involve Foreman

Implementation stage
  -> route through Foreman when available
  -> Foreman may answer directly or escalate to the human
  -> resolved answers are delivered back to the live implementation harness
```

Question tools should always be routed. `ask_foreman` is only one source. Harness-native/default question tools must be integrated too.

## Question sources

Supported and target question sources are:

- `ask_foreman`, the existing Substrate explicit escalation tool.
- Claude Code's ask-user-question tool.
- OMP's native `ask` tool.
- OpenCode's native `question.asked` event.
- Future harness-native question mechanisms that can expose a pending answer handle.

All sources normalize to the same internal event shape.

```text
AgentQuestionAsked
  stage: planning | implementation
  source: ask_foreman | claude_ask | omp_ask | opencode_question | future_harness_question
  sessionID
  question payload
  pendingAnswerHandle
```

The normalized payload must support both free-text questions and structured option questions.

## Planning-stage behavior

During planning, a question means:

> The planner needs a human decision before it can continue.

The flow is:

```text
User starts planning
  ↓
Planning agent runs
  ↓
Agent asks using any available question mechanism
  ↓
Harness or bridge emits AgentQuestionAsked(stage=planning)
  ↓
Planning service routes the question directly to the human
  ↓
TUI shows a Planning Question action/overlay
  ↓
User answers
  ↓
Substrate delivers the answer to the live planning session
  ↓
Planner resumes and incorporates the answer into the plan
  ↓
Plan artifact becomes the durable record of the decision
```

There is no Foreman in this flow. Substrate must not start a planning Foreman.

### Planning storage policy

Planning questions and answers must not be written to the Foreman FAQ and must not be carried into implementation as separate Foreman context.

The answer exists to inform the produced plan. If the decision matters, the planner must encode it into the plan. The approved plan is the source of truth for implementation.

Operationally, Substrate may persist enough state to support the live waiting-for-answer UI and session transcript. That operational state is not product FAQ and is not implementation guidance.

## Implementation-stage behavior

During implementation, all agent questions go through Foreman first, regardless of which tool produced the question.

```text
Implementation agent runs
  ↓
Agent asks using any available question mechanism
  ↓
Harness or bridge emits AgentQuestionAsked(stage=implementation)
  ↓
Implementation service routes the question to Foreman
  ↓
Foreman answers directly or escalates
  ↓
If Foreman answers:
    answer is delivered to the live implementation agent
  ↓
If Foreman escalates:
    TUI asks the human
    human answer resolves the escalation
    answer is delivered to the live implementation agent
```

This keeps implementation behavior coherent. The implementation stage uses Foreman as the first-line resolver; the planning stage uses the human directly.

## UX requirements

### Planning copy

Planning questions should use planner/human language.

Example:

```text
Planning question

The planner needs your input before it can continue.

Question:
Should this be a full cutover or preserve a compatibility alias?

Reply to planner…
```

Do not show Foreman copy for planning questions.

Avoid:

```text
Foreman question
Reply to Foreman…
```

### Implementation copy

Implementation questions should use Foreman language only when Foreman actually participates.

Example:

```text
Foreman escalated a question

The implementation agent asked:
Should this migration preserve compatibility aliases?

Foreman could not answer confidently.
Reply to Foreman…
```

If Foreman answers directly, no user overlay is needed.

### Structured question UX

Claude Code and OMP support structured question tools. Substrate must preserve that structure instead of degrading every question to a single free-text textarea.

The question UI must be able to represent:

- one or more questions;
- option lists;
- multi-select questions;
- recommended/default options;
- custom free-text answers when supported by the tool contract;
- optional annotations or previews when a harness provides them.

Free-text `ask_foreman` questions can continue to use a simple textarea-style reply.

## Harness research findings

This section is based on the installed/current package sources in this repository.

### Claude Code

Evidence:

- `bridge/node_modules/@anthropic-ai/claude-agent-sdk/sdk-tools.d.ts`
- `bridge/node_modules/@anthropic-ai/claude-agent-sdk/sdk.d.ts`

Claude Code exposes an `AskUserQuestionInput` / `AskUserQuestionOutput` tool shape.

The input shape supports 1-4 questions. Each question includes:

- `question`: the complete question text;
- `header`: a short label;
- `options`: 2-4 choices;
- multi-select support;
- recommended/default-style metadata;
- richer output support for annotations and previews.

The output shape includes:

- the questions that were asked;
- `answers`, keyed by question text;
- optional annotations.

The SDK type surface also includes `ToolConfig.askUserQuestion`, indicating the ask-user-question capability is configurable by SDK consumers.

Product implication: Claude's normal question path is structured. Substrate's normalized question model and TUI answer path must preserve the structured shape and return the answer format Claude expects.

### OMP

Evidence:

- `bridge/node_modules/@oh-my-pi/pi-coding-agent/src/tools/ask.ts`
- `bridge/node_modules/@oh-my-pi/pi-coding-agent/src/session/agent-session.ts`

OMP has a built-in `ask` tool intended for interactive user prompting during execution.

Its input shape is:

```text
questions: [
  {
    id,
    question,
    options: [{ label }],
    multi?,
    recommended?
  }
]
```

It supports:

- multiple questions;
- option selection;
- multi-select;
- recommended option;
- automatic `Other (type your own)` support;
- configurable timeout;
- timeout disabled in plan mode.

OMP's plan-mode prompt and enforcement refer to the native tool name `ask`. The planner is expected to use `ask` or `exit_plan_mode` during planning. In current Substrate bridge wiring, OMP's native `ask` appears not to be exposed because the bridge allows a selected tool list plus the custom `ask_foreman` tool. That must change for native/default OMP question support.

Product implication: OMP's `ask` should be exposed and intercepted as a first-class question source. In planning it routes directly to the human. In implementation it routes through Foreman.

### OpenCode

Current repo behavior already maps OpenCode's native `question.asked` event into an adapter question event, and `SendAnswer` replies through OpenCode's pending question endpoint.

Product implication: OpenCode already has the native question/answer primitives needed. The missing piece is stage-aware routing.

### Codex

Current repo behavior runs Codex through `codex exec --full-auto`, and the Codex harness does not support live `SendAnswer`.

Product implication: Codex cannot support live interactive planning questions until the harness has a real question/answer channel. Codex planning should instead surface unresolved questions in the plan artifact.

## Product requirements

1. Normalize every agent question mechanism into `AgentQuestionAsked`.
2. Route by session stage, not by tool name.
3. During planning, route directly to the human and deliver the answer to the live planning harness.
4. During planning, do not store question/answer pairs as Foreman FAQ and do not feed them to implementation Foreman context.
5. During implementation, route all question sources through Foreman first when Foreman is available.
6. Preserve structured question semantics for Claude Code and OMP.
7. Answer delivery must unblock the pending live harness question/tool call.
8. Unsupported harnesses must fail honestly and must not pretend an answer will resume the agent.

## Implementation plan

### Phase 1: Define the normalized question contract

Introduce or formalize an adapter-level question payload that can represent both existing free-text questions and structured native ask questions.

Conceptual shape:

```text
AgentQuestion
  id
  sessionID
  stage
  source
  freeText?
  structured?
  pendingAnswerHandle
```

Structured shape:

```text
StructuredQuestionSet
  questions: [
    {
      id?
      question
      header?
      options: [{ label, preview? }]
      multiSelect
      recommendedIndex?
    }
  ]
  supportsCustomAnswer
  supportsAnnotations
```

Answer shape:

```text
AgentQuestionAnswer
  text?                         # free-text answers
  structuredAnswers?            # question id/text -> selected/custom answer
  annotations?
```

Implementation details:

- Keep the existing `adapter.AgentEvent{Type: "question"}` concept if possible, but extend the payload rather than adding parallel event types.
- Preserve backend-specific correlation data needed for `SendAnswer` or equivalent harness reply calls.
- Ensure all errors in question routing and answer delivery are logged with `slog` and include the original error.

Acceptance criteria:

- A single internal question event can represent `ask_foreman`, OMP `ask`, Claude ask-user-question, and OpenCode `question.asked`.
- Existing `ask_foreman` implementation questions still fit the model without structured fields.

### Phase 2: Add a stage-aware question router

Create a single routing decision point used by planning and implementation services.

Routing policy:

```text
if stage == planning:
  route directly to human
else if stage == implementation:
  route through Foreman when available
else:
  return an explicit unsupported-stage error
```

Planning route responsibilities:

- mark the planning session/work item as waiting for answer;
- surface the question in the TUI;
- wait for the human answer;
- deliver the answer back to the live planning harness through `SessionRegistry.SendAnswer` or the harness-specific answer channel;
- resume planning.

Implementation route responsibilities:

- send all question sources to Foreman first;
- let Foreman answer or escalate;
- deliver the final answer back to the pending implementation harness question;
- preserve existing implementation-stage FAQ/escalation semantics.

Acceptance criteria:

- The router, not the tool name, decides whether a question goes to the human or Foreman.
- Planning never attempts to route through Foreman.
- Implementation no longer only routes `ask_foreman`; native question events use the same Foreman path.

### Phase 3: Wire planning event forwarding

Planning currently waits for turn completion and handles `done`/`error`. It must also consume question events for the lifetime of the planning turn.

Implementation tasks:

- Add planning event forwarding analogous to implementation forwarding, but with planning-stage policy.
- Ensure `question` events do not get dropped while the planner is blocked waiting for an answer.
- Ensure planning remains in `waiting_for_answer` until the answer is delivered or the session is aborted.
- Ensure abort/cancel paths resolve or clean up pending questions honestly.

Acceptance criteria:

- If a planning agent calls `ask_foreman`, the TUI shows a planning question and answering resumes the planner.
- If a planning harness emits a native question event, the same planning question UI appears.
- No timeout fallback silently invents an answer by default.

### Phase 4: Fix the answer delivery command path

The human answer command must always deliver the answer to the live pending harness when the question has a pending session answer handle.

Implementation tasks:

- Update the direct-human answer path so it calls `SessionRegistry.SendAnswer` for planning questions.
- Keep Foreman resolution for implementation-stage escalations.
- Avoid relying on `QuestionService.Answer` alone; persistence without live delivery is not a successful answer.
- Return explicit user-visible failure if the harness does not support answer delivery.

Acceptance criteria:

- Answering a planning question unblocks the bridge/tool call.
- Answering an implementation escalation continues to work through Foreman.
- Codex or other unsupported harnesses report that live answering is unsupported instead of appearing to hang.

### Phase 5: Integrate `ask_foreman` with the normalized router

Keep `ask_foreman` available as a question source, but stop treating it as inherently Foreman-bound.

Implementation tasks:

- Ensure bridge-emitted `ask_foreman` question events include enough source metadata to identify `source=ask_foreman`.
- Route the event by stage.
- In planning, display as `Planning question`, not `Foreman question`.
- In implementation, route to Foreman as before.

Acceptance criteria:

- `ask_foreman` in planning reaches the user directly.
- `ask_foreman` in implementation reaches Foreman first.
- Transcript/UI labels are product-accurate for each stage.

### Phase 6: Integrate OMP native `ask`

OMP's native `ask` tool is the default planning question mechanism for OMP. It must be exposed and routed.

Implementation tasks:

- Adjust OMP bridge tool exposure so native `ask` is available where planning/implementation agents are expected to ask questions.
- Intercept OMP `ask` tool calls before the default OMP UI path handles them, because Substrate owns the TUI and stage routing.
- Convert OMP `ask` input into the normalized structured question payload.
- Convert Substrate's structured answer back into the OMP `ask` tool result format.
- Preserve multi-question, option, multi-select, recommended, and custom-answer behavior.

Acceptance criteria:

- OMP planner can call `ask` during planning and the Substrate TUI asks the human.
- The answer returned to OMP has the shape its `ask` tool expects.
- OMP implementation questions route through Foreman first.

### Phase 7: Integrate Claude Code native ask-user-question

Claude Code exposes a structured ask-user-question tool shape in its SDK types. Substrate should enable and intercept it as a native question source.

Implementation tasks:

- Confirm the runtime tool name emitted by Claude Code for `AskUserQuestionInput` in the current bridge runtime.
- Enable the ask-user-question capability in Claude bridge configuration if it is not already active.
- Intercept ask-user-question tool calls or SDK control events and emit the normalized structured question event.
- Convert Substrate answers into `AskUserQuestionOutput`, preserving answers keyed by question text and any supported annotations.
- Keep bridge parity with OMP where backend capabilities align.

Acceptance criteria:

- Claude planner can use its normal question tool during planning.
- The TUI preserves the structured options and returns Claude-compatible output.
- Claude implementation questions route through Foreman first.

### Phase 8: Preserve and extend OpenCode native question handling

OpenCode already provides native `question.asked` events and an answer endpoint.

Implementation tasks:

- Ensure OpenCode question events include `stage` and `source=opencode_question` metadata in the normalized event.
- Route OpenCode planning questions directly to the human.
- Route OpenCode implementation questions through Foreman.
- Ensure `SendAnswer` still replies to the correct OpenCode request ID.

Acceptance criteria:

- OpenCode planning questions are answerable in the TUI and resume planning.
- OpenCode implementation questions go through Foreman first.

### Phase 9: Update TUI question surfaces

The TUI must display question source and stage accurately.

Implementation tasks:

- Make question overlay copy stage-aware.
- Add structured option-selection UI for native ask questions, reusing existing selection patterns where possible.
- Ensure multi-question flows can be answered without losing intermediate selections.
- Ensure rendered question overlays respect terminal width/height, including narrow-size cases.
- Update transcript rendering to show `Planner asked` for planning questions and Foreman escalation language only when Foreman participates.

Acceptance criteria:

- Planning questions never say `Reply to Foreman`.
- Structured questions render as choices, not raw JSON.
- Width/height layout tests cover narrow terminal sizes.

### Phase 10: Codex unsupported behavior

Codex currently has no live answer channel.

Implementation tasks:

- Keep Codex from advertising interactive question support unless a real answer channel is added.
- If a Codex planning session produces unresolved textual questions, instruct the planner to record them in the plan instead of waiting for a live answer.
- Ensure unsupported answer delivery errors are surfaced clearly.

Acceptance criteria:

- Codex does not present a fake interactive answer UI.
- Users get an honest message when live answer delivery is unsupported.

### Phase 11: Tests and verification

Add tests that prove routing and answer delivery behavior.

Test coverage:

- Planning `ask_foreman` routes directly to human and calls session answer delivery.
- Planning native structured question routes directly to human and returns structured answer output.
- Implementation `ask_foreman` routes through Foreman.
- Implementation native question routes through Foreman.
- Planning questions are not appended to Foreman FAQ or implementation context.
- OMP `ask` input/output conversion preserves options, multi-select, recommended, and custom answer behavior.
- Claude ask-user-question conversion preserves expected input/output shape.
- OpenCode request ID correlation survives routing.
- Unsupported harness answer delivery reports a clear error.
- TUI planning question copy and structured layouts fit narrow terminal dimensions.

Verification commands should include targeted package tests plus `go build ./...` when LSP or generated integration points are involved.

## Rollout order

Recommended implementation order:

1. Normalize question payload and answer shape.
2. Add stage-aware router.
3. Wire planning forwarding and answer delivery for existing `ask_foreman`.
4. Route implementation native question events through Foreman.
5. Integrate OpenCode stage-aware handling.
6. Integrate OMP native `ask`.
7. Integrate Claude Code native ask-user-question.
8. Update TUI structured-question UX and copy.
9. Add/finish tests and run verification.

This order gets the broken planning `ask_foreman` path working first, then expands to native question tools without changing the product model.
