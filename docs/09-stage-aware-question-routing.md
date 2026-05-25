# Stage-Aware Agent Question Routing

<!-- docs:last-integrated-commit 10e50295fb75f72c67233e191ae34fb8fc091f1e -->

## Purpose

Substrate agents can ask questions through multiple mechanisms. The explicit escalation tool was historically the implementation-agent escalation path, but planning agents can also see it even though no Foreman runs during planning. Harnesses have native question mechanisms too.

This document defines product behavior for routing all agent question mechanisms consistently.

## Product definition

Every agent question mechanism normalizes to one event: `agent_question.raised`. The source does not determine routing. The session stage determines routing.

**Planning stage:** Route directly to human → deliver answer to live planning harness. No Foreman involvement.

**Review stage:** Route through Foreman (same as Implementation) → Foreman answers directly or escalates → deliver answer to live review harness.

**Implementation stage:** Route through Foreman when available → Foreman answers directly or escalates → deliver answer to live implementation harness.

## Question sources

All sources normalize to: `agent_question.raised` with stage, source, sessionID, question payload, and pendingAnswerHandle. Payload must support free-text and structured option questions.

## Planning-stage behavior

Planning questions mean: the planner needs a human decision before continuing.

Flow: User starts planning → Agent runs → Asks via any mechanism → `agent_question.raised(stage=planning)` → Routes to human → TUI shows Planning Question overlay → User answers → Answer delivered to live session → Planner resumes → Plan artifact records decision.

There is no Foreman. Substrate must not start a planning Foreman. Do not write planning Q&A to Foreman FAQ or carry into implementation as Foreman context. The approved plan is the source of truth.

## Implementation-stage behavior

All agent questions go through Foreman first, regardless of tool.

Flow: Agent runs → Asks via any mechanism → `agent_question.raised(stage=implementation|review)` → Routes to Foreman → Foreman answers directly OR escalates → Answer delivered to live agent.

Implementation uses Foreman as first-line resolver; planning uses the human directly.

## UX requirements

### Planning copy

Use planner/human language. Example:

```
Planning question

The planner needs your input before it can continue.

Question: Should this be a full cutover or preserve a compatibility alias?

Reply to planner…
```

Never show Foreman copy for planning questions.

### Implementation copy

Use Foreman language only when Foreman participates:

```
Foreman escalated a question

The implementation agent asked: Should this migration preserve compatibility aliases?

Foreman could not answer confidently.
Reply to Foreman…
```

If Foreman answers directly, no user overlay is needed.

### Structured question UX

Preserve structured question structure. The UI must represent: one or more questions, option lists and multi-select, recommended/default options, custom free-text answers when supported, and annotations when provided by harness.

## Product requirements

1. Normalize every agent question mechanism into `agent_question.raised`.
2. Route by session stage, not by tool name.
3. During planning, route directly to the human and deliver the answer to the live planning harness.
4. During planning, do not store question/answer pairs as Foreman FAQ and do not feed them to implementation Foreman context.
5. During implementation, route all question sources through Foreman first when Foreman is available.
6. Preserve structured question semantics for harnesses that support them.
7. Answer delivery must unblock the pending live harness question/tool call.
8. Unsupported harnesses must fail honestly and must not pretend an answer will resume the agent.
