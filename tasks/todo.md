# Task: Redesign GitHub/GitLab repo adapters around runtime repository coordinates

## Plan
- [ ] Define the target architecture and invariants for provider auth, work-item identity, and repo lifecycle identity.
- [ ] Introduce provider-neutral repository coordinate types in the domain and event payloads.
- [ ] Refactor work-item adapters so GitHub and GitLab issue selection, fetch, update, and commenting use item-carried repository identity rather than configured repo/project identity.
- [ ] Refactor repo lifecycle wiring and adapters so PR/MR creation uses explicit base/head repository coordinates resolved from workspace remotes and selected item context.
- [ ] Remove fixed owner/repo/project configuration requirements and simplify adapter construction around auth + host only.
- [ ] Add end-to-end tests covering same-repo flows, fork flows, and cross-project tracker flows.
- [ ] Verification: targeted unit tests for domain serialization, adapter behavior, repo lifecycle resolution, and GitHub/GitLab fork-aware review creation.

## Progress Notes
2026-03-08 - Planning only. User explicitly wants full-cutover design with no backwards-compat constraints.

## Review
Pending.
