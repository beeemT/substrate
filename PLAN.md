# Plan: Enable Full Git Operations Inside the Sandbox

## Problem

The sandbox blocks git commits, signing, and push because:

1. **`.git`/`.bare` write access denied** — The sandbox allows writes only to `workDir`, `sessionTmpDir`, `configDir`, and `bunCacheDir`. Git needs write access to the object store, index, refs, and hooks.
2. **No network access on macOS** — `sandbox-exec` denies all network by default. `git push` (SSH/HTTPS) is blocked.
3. **SSH/GPG signing agents unreachable** — The sandbox does not forward `SSH_AUTH_SOCK` or allow writes to `~/.gnupg`. Signing commits or pushing via SSH keys (including 1Password's SSH agent) fails.

## Approach

Expand the sandbox profile to allow the agent to perform normal git operations: commit (with signing), push, fetch. The agent already has write access to the worktree — git metadata is the same trust boundary.

### 1. Allow write access to git directory (targeted)

**File:** `internal/adapter/bridge/runtime.go`

Add a new `gitDir` parameter to `BuildSandboxCmd`. Only the git directory gets write access — not the repo root, not other worktrees.

| Repo type | `gitDir` value | Location |
|-----------|---------------|----------|
| git-work | `<repoRoot>/.bare/` | Sibling of worktree parent; contains shared objects, refs, hooks, and worktree-specific metadata under `worktrees/<name>/` |
| plain git | `""` (empty) | `.git/` is already inside `workDir` — no additional allow needed |

The `gitDir` is resolved by a new `ResolveGitDir(workDir)` function that checks for `.bare/` in the parent directory. Callers pass the result to `BuildSandboxCmd`.

### 2. Allow network access on macOS

Add `(allow network*)` to the `sandbox-exec` profile. This enables `git push` and `git fetch` over SSH and HTTPS. Linux `bwrap` already allows network by default.

### 3. Forward SSH agent socket

Detect `SSH_AUTH_SOCK` env var at profile build time. If set, allow write access to the socket path. This enables:
- SSH key authentication for push (including 1Password SSH agent)
- SSH-based commit signing

### 4. Allow GPG directory access

Allow writes to `~/.gnupg/` when the directory exists. This enables GPG commit signing via `gpg-agent`.

### 5. Update callers

**Files:**
- `internal/adapter/claudeagent/harness.go`
- `internal/adapter/ohmypi/harness.go`

Both callers call `bridge.ResolveGitDir(workDir)` and pass the result to `BuildSandboxCmd`.

## Security Boundary

The sandbox still isolates the agent to its own worktree. The additional allows are:

| Allow | Scope | Justification |
|-------|-------|---------------|
| `gitDir` writes | `.bare/` only | Required for git commits; shared across worktrees but inherent to git |
| Network | All outbound | Required for push/fetch |
| `SSH_AUTH_SOCK` | Single socket file | Required for SSH auth and signing |
| `~/.gnupg/` | GPG home only | Required for GPG signing |

The agent cannot write to other worktrees, the repo root, or arbitrary home directory locations. Git identity (user.name, user.email) is not configured by us — the user's existing git config is used.

## Known Limitation: SSH `known_hosts`

SSH to a new host requires accepting the host key. The sandbox does not allow writes to `~/.ssh/` (to avoid exposing private keys to writes). If the agent pushes to a host not already in `known_hosts`, SSH will fail. Workarounds:
- Set `GIT_SSH_COMMAND="ssh -o StrictHostKeyChecking=no"` in the environment
- Pre-populate `~/.ssh/known_hosts` with expected host keys

## File Changes Summary

| File | Change |
|------|--------|
| `internal/adapter/bridge/runtime.go` | Add `ResolveGitDir`; add `gitDir` param; allow git dir writes, network, SSH agent, GPG |
| `internal/adapter/claudeagent/harness.go` | Pass `bridge.ResolveGitDir(workDir)` to `BuildSandboxCmd` |
| `internal/adapter/ohmypi/harness.go` | Pass `bridge.ResolveGitDir(workDir)` to `BuildSandboxCmd` |
| `internal/adapter/bridge/bridge_test.go` | Add tests for `ResolveGitDir` |
