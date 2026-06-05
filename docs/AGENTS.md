# Docs Guidelines

These conventions keep documentation stable, navigable, and aligned with Substrate's product and architecture rather than its current implementation state.

---

## 1. Every doc gets a last-integrated-commit marker

Add this HTML comment immediately after the top-level heading:

```html
<!-- docs:last-integrated-commit <git-sha> -->
```

This pins the doc to the git commit it was verified against. Update it whenever the doc is meaningfully revised. All docs must have this marker — it is a hard requirement.

---

## 2. Product and architecture first; implementation second

Docs describe **what the system does and why**, not **how it is implemented**. Specifically:

- **What to write:** domain types and their invariants, state machine transitions, workflow narratives, ownership boundaries, API contracts, UX behavior, product requirements.
- **What to avoid:** internal method signatures, struct field names, package-level plumbing, temporary scaffolding, "how we built this" as primary content.

When implementation detail is necessary — for example, a validation rule or a hard limit — write it as an acceptance criterion or a rationale, not as a code reference.

---

## 3. One concern per document

Each doc owns one coherent area. Cross-references connect documents rather than one doc trying to cover everything.

Core docs:
- `00-overview.md` — mission, workflow, design principles
- `01-domain-model.md` — domain types, state machines, relationships, invariants
- `02-layered-architecture.md` — layer diagram, service responsibilities, transactional pattern
- `03-event-system.md` — event catalog, bus semantics, publish flow
- `04-adapters.md` — adapter roles and contracts
- `05-orchestration.md` — workflow runtime (planning, implementation, review, recovery)
- `06-tui-design.md` — operator-facing TUI behavior
- `07-implementation-plan.md` — phased roadmap and risk register
- `08-tui-design-system.md` — visual design system and layout contracts
- `09-stage-aware-question-routing.md` — product behavior for question routing
- `10-foreman-lifecycle.md` — ownership boundary for Foreman runtime
- `11-settings.md` — settings system interaction model
- `12-agent-session-graph-continuation.md` — graph-driven resume/retry/follow-up, continuation lifecycle, kind routing
- `13-electron-app-plan.md` — additive future direction

---

## 4. Opening paragraph sets the scope and contract

Every doc starts with a short purpose statement — one to three sentences. State what the doc covers and, where relevant, what it explicitly does not cover. This anchors the reader and prevents scope creep within a doc.

---

## 5. Diagrams serve as truth, not decoration

Mermaid state diagrams, flowcharts, and ERDs are first-class content. They describe behavior and relationships that are authoritative in the doc. Keep them accurate; stale diagrams are worse than no diagrams.

---

## 6. Use tables for enumerated values and key-value facts

Domain enums, state values, configuration options, and comparison tables belong in tabular form, not prose lists.

---

## 7. Link to other docs by purpose, not by file path

Write `see §4 Foreman Handling` or `as defined in the domain model` rather than `see 05-orchestration.md`. Readers should not need to know the file naming convention.

---

## 8. Structural elements that age well

Favor these over prose when applicable:
- State machine diagrams for lifecycle descriptions
- Ownership boundary diagrams for orchestration contracts
- Acceptance criteria lists for validation rules
- Concrete UX copy examples for user-facing behavior
- Risk registers for phased work or future directions

---

## 9. What to cut

When revising, remove:
- Implementation scaffolding ("we will add X"), unless the doc is explicitly a roadmap
- Internal field names and type references unless the doc is a schema reference
- Method-level detail that belongs in code comments
- Stale "how we built it" narratives that no longer match the code
- Repetition that could be a cross-reference
