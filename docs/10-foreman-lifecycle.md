# 10 - Foreman Lifecycle Ownership

<!-- docs:last-integrated-commit 10e50295fb75f72c67233e191ae34fb8fc091f1e -->

> **Status:** The orchestrator layer fully owns the Foreman lifecycle. The TUI never directly owns or controls Foreman instances. Service providers expose `Close(context.Context)` which stops all foremen, aborts sessions, and closes the event bus.

---

## 1. Summary

The orchestrator owns Foreman lifecycle management. The TUI interacts only through orchestrator operations — it calls orchestrator commands, never Foreman itself. The TUI learns Foreman state via database polls and read-only accessor methods; it never holds a direct reference to a Foreman instance.

---

## 2. Ownership Boundary

```
┌─────────────────────────────────────────────────────┐
│                       TUI (Views)                    │
│  Reads Foreman state via read-only accessors         │
│  Calls orchestrator operations for all Foreman       │
│  lifecycle transitions                               │
└─────────────────────────┬───────────────────────────┘
                          │ orchestrator ops
                          ▼
┌─────────────────────────────────────────────────────┐
│                  Orchestrator Layer                  │
│                                                      │
│  AnswerRouter — routes human answers and skips       │
│  ImplementationService — BeginForeman / EndForeman  │
│  ReviewFollowup — owns Foreman lifecycle for         │
│                   follow-up sessions                │
│  QuestionRouter — routes questions to Foreman or     │
│                   escalates to human                │
└─────────────────────────┬───────────────────────────┘
                          │
                          ▼
┌─────────────────────────────────────────────────────┐
│                  Foreman (runtime)                   │
│  Persistent harness session holding plan + FAQ      │
│  Two-tier resolution: auto-answer OR escalate        │
└─────────────────────────────────────────────────────┘
```

The orchestrator is the single owner of Foreman Start and Stop. The TUI has no Foreman pointer and no direct Foreman calls.

---

## 3. Foreman Model

The Foreman is a persistent harness session that holds:

- the approved plan (orchestrator section + all sub-plans)
- accumulated FAQ / answered questions
- prior Foreman conversation context

Questions are serialized through that single session so later answers can rely on earlier resolved context. The Foreman session is stopped when implementation completes and restarted when follow-up feedback is provided. It appears as a live session in the TUI status bar.

---

## 4. Two-Tier Resolution

When a question arrives during implementation:

1. **Foreman turn** — the question is presented to the Foreman session
2. **Confidence check** — the Foreman returns either high confidence or uncertainty
3. **High confidence** — Substrate auto-answers and appends the question and answer to the plan FAQ
4. **Uncertain** — Substrate escalates to the human with the Foreman's proposed answer; the human reviews and may iterate with the Foreman before approving

Every answered question is appended to the plan FAQ so later sessions and reviews inherit the clarified decision.

---

## 5. Answer Timeout

If no proposed answer arrives within a configurable window (default 0 = indefinite wait), the Foreman re-queues the question to the priority front and restarts the Foreman session with current plan and FAQ. If repeated immediate restarts suggest the question no longer fits in the usable context window, escalate directly to the human. This prevents a blocked Foreman from stalling the work item.

---

## 6. Recovery

If the Foreman session dies while answering a question:

1. Re-queue the in-flight question at the front of the priority queue
2. Restart the Foreman with the current plan and FAQ as context
3. Deliver the re-queued question first

If repeated immediate restarts suggest the question no longer fits in the usable context window, escalate directly to the human. The TUI observes restart events and reflects Foreman state through polling.

---

## 7. Referenced Documents

- Foreman semantics and two-tier resolution: `05-orchestration.md §4`
- TUI interaction model for questions: `06-tui-design.md`
