# 04 — Adapter Implementations

<!-- docs:last-integrated-commit 10e50295fb75f72c67233e191ae34fb8fc091f1e -->

The adapter package defines four roles that bridge the application core to external systems:

- **WorkItemAdapter** — tracker-style sources used during session creation and tracker event hooks.
- **RepoLifecycleAdapter** — repository event handlers for MR/PR automation once a worktree exists.
- **AgentHarness** — execution backends for planning, implementation, review, and foreman sessions.
- **HarnessActionRunner** — executes short-lived control-plane actions (login, auth checks) routed through the harness.

---

## Shared contracts

### WorkItemAdapter

`Resolve(...)` selects work items and returns a `Session` populated with source metadata. `Fetch(...)` rehydrates a stored session from its external ID. `ListSelectable(...)` browses available items with UI-driven filtering. `Watch(...)` streams state-change events (`created`, `updated`, `error`). `UpdateState(...)` mutates the tracker representation. `AddComment(...)` posts a comment. `OnEvent(...)` reacts to `plan.approved` (moves item to `in_progress`) and `work_item.completed` (moves to `done`).

### RepoLifecycleAdapter

Responds to worktree lifecycle events: `worktree.created` creates the review artifact; `worktree.reused` updates the body with current plan text; `work_item.completed` marks the artifact complete. Platform routing prevents GitHub and GitLab adapters from reacting to each other's payloads.

### AgentHarness

`StartSession` returns a session object that supports streaming tokens, `SendMessage` for multi-turn messaging, `Steer` to interrupt streaming with a steering prompt, and `Compact` to request context compaction. Harnesses that lack steering or compaction return an error when those methods are called. `SupportsNativeResume` advertises file-based session resume. `SupportedTools` lists exposed tool names. `CommitConfig` (injected into implementation sessions) controls commit strategy, format, and template.

Session metadata emitted by each harness is persisted generically and used to restore the session on follow-up.

### Dual-write pattern

All three repo-lifecycle adapters use a shared dual-write when persisting PR/MR state: records an audit event, upserts into the provider-specific table, and creates a cross-provider link between the work item and the review artifact. Per-reviewer review state and CI check status are also tracked, both populated by the refresh loop.

### PR/MR refresh loop

No webhooks. A 120-second ticker is the only inbound channel from the remote platform. On each tick, the adapter calls `ListNonTerminal` and re-fetches state for every open PR/MR: top-level state, per-reviewer review list, and CI/check status. If a reviewer's state differs, emits `pr.review_state_changed`; if a check fails, emits `pr.ci_failed`. On reaching a terminal state, cleans up stale rows. On merge, checks all linked artifacts; if all are merged and the work item is `SessionCompleted`, transitions to `SessionMerged` and emits `pr.merged`. Failures are logged and do not block.

### Review-comment fetcher

Comment bodies are never persisted — fetched on demand at follow-up time. `ReviewCommentDispatcher` routes by provider to the registered fetcher (exposed as `Services.ReviewComments`). GitHub fetches unresolved threads in a single GraphQL call and filters out resolved ones. GitLab fetches unresolved discussions; inline notes include path and line; top-level discussions populate the `General` section.

### Post-merge automation

When enabled, the adapter subscribes to `pr.merged` and closes the linked tracker issue (parsed from the work item's external ID). Already-closed issues are a no-op.

### Plan-approval description sync

On `plan.approved`, the adapter posts the approved plan as a comment on the source issue. It then patches the description of every open PR/MR linked to the work item with the approved plan text. Closed and merged artifacts are skipped.

---

## Work item adapters

**Manual** — Always available. `Resolve(...)` accepts a manual selection and creates a session with source `manual` and sequential workspace-scoped IDs (`MAN-1`, `MAN-2`, …). All other methods are no-ops.

**Linear** — Supports browsing and watching issues, projects, and initiatives; mutating issue state and comments. Watch polls assigned issues with exponential backoff on rate limits. `plan.approved` moves the issue to `in_progress`; `work_item.completed` moves it to `done`.

**GitLab** — Work-item adapter only; repository lifecycle is handled by the separate `glab` adapter. Supports browsing issues, milestones, and epics. Watch polls assigned open issues. `plan.approved` posts plan comments and moves the primary issue to `in_progress`; `work_item.completed` moves it to `done`.

**GitHub** — The only dual-role adapter (both `WorkItemAdapter` and `RepoLifecycleAdapter`). Supports browsing and watching issues and milestones. Watch polls assigned open issues. `plan.approved` posts plan comments and moves the primary issue to `in_progress`. Repository lifecycle handles worktree creation, reuse, and completion events.

**Sentry** — A source-only adapter: it browses and watches Sentry issues during session creation but does not update state, post comments, or participate in repository lifecycle automation. It does not support projects, initiatives, mutations, or any worktree/PR/MR automation. Watch polls assigned issues with exponential backoff on rate limits. Treat it as read-only.

---

## Repo lifecycle adapters

**glab** — GitLab repository lifecycle, separate from the GitLab work-item adapter. `worktree.created` creates a draft MR (or discovers an existing one); `worktree.reused` updates the description with the current plan text; `work_item.completed` un-drafts the MR and records final state. Configured reviewers and labels are forwarded to MR creation.

**GitHub** — Handled by `GithubAdapter` itself, routed to prevent cross-platform event leakage. `worktree.created` creates a draft PR; `worktree.reused` updates the body; `work_item.completed` runs completion handling.

Both use the dual-write pattern for persistence.

**Remote detection** — The application discovers git repositories in the workspace, detects each platform (GitHub, GitLab, or unknown), and registers a routed lifecycle adapter per platform. Unknown remotes log warnings. Sentry config is ignored for lifecycle registration.

---

## Harnesses

Four harnesses are shipped. All support streaming and `SendMessage`.

| | Steering | Compaction | Native Resume |
|---|---|---|---|
| Oh My Pi | yes | yes | yes |
| Claude Agent | yes | yes | yes |
| Codex | no | no | yes |
| ACP | conditional | conditional | conditional |

**Steering** — Injects a prompt into active streaming. Oh My Pi, Claude Agent, and ACP expose this. Codex does not support steering.

**Compaction** — Requests context window compaction. Supported unconditionally by Oh My Pi and Claude Agent; conditional for ACP (based on agent capabilities); unsupported by Codex.

**Native resume** — Oh My Pi, Claude Agent, and Codex resume from saved session files. ACP uses `session/resume` or `session/load` when advertised by the agent.

**OMP bridge** additionally surfaces `retry_wait` and `retry_resumed` events through the agent event channel. The Claude Agent bridge inherits the user's existing Claude Code auth and settings; foreman sessions restrict the agent to read-only tools and omit the `ask_foreman` MCP server.
