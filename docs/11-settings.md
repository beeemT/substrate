# Settings System

<!-- docs:last-integrated-commit 2826f9fd2e658941eb96072a0c30df9766b92d94 -->

Full-screen configuration covering commit strategy, planning, review, Foreman, harness routing, provider auth, and repository lifecycle.

With the daemon/TUI split, settings have two ownership domains. The connected daemon owns runtime/product configuration and applies saves by rebuilding its service graph. The local TUI owns presentation defaults plus daemon registry/selection metadata. During the transition, some local TUI fields still round-trip through `SettingsAPI`; those paths must preserve token refs and last-seen metadata until the ownership split is complete.

## Page Structure
```
┌─ Settings ───────────────┬─────────────────────────────────────────┐
│ ▼ Home View               │ Home View                                │
│ ▼ Commit                  │ ────────────────────────────────────────│
│ ▼ Repo Documentation      │ Docs repos/folders read before planning. │
│ ▼ Review                  │ Relative paths resolve against workspace.│
│ ▼ Harness Routing         │                                          │
│ ▼ Provider · GitHub       │ Current: docs/reference, /srv/eng-docs  │
│ ▼ Provider · Linear       │                                          │
│ ▼ Provider · GitLab       │                                          │
├───────────────────────────┴─────────────────────────────────────────┤
│ [↑↓] navigate  [→] expand  [←] collapse  [enter] focus  [esc] close  [t] test  [r] reveal │
└─────────────────────────────────────────────────────────────────────┘
```
**Left**: Collapsible navigation tree. **Right**: Field editor with description and options.

## Key Bindings

### Tree Focus
| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate sections |
| `→` | Expand / focus fields |
| `←` | Collapse / parent |
| `Enter` | Focus fields |
| `Esc` | Close (confirms if dirty) |

### Field Focus
| Key | Action |
|-----|--------|
| `↑` / `↓` | Navigate fields |
| `Enter` / `e` | Edit field |
| `Space` | Toggle boolean |
| `←` / `Esc` | Return to tree |
| `t` | Test connectivity |
| `g` | Login (if supported) |
| `r` | Reveal/hide secret |

> **Note:** Settings auto-save when navigating away with dirty state. Explicit save key removed.

### Edit Mode
| Key | Action |
|-----|--------|
| `Enter` | Save |
| `Esc` | Cancel |

## Dirty State and Exit Confirmation
Edits mark the page dirty. `Esc` with unsaved changes shows:
```
┌─ Unsaved Changes ─────────────────────────────────────────┐
│  You have unsaved changes. Save before closing?             │
│  [Enter/y] Save  [n/Esc] Discard                          │
└────────────────────────────────────────────────────────────┘
```
| Key | Action |
|-----|--------|
| `Enter` / `y` | Save and close |
| `n` / `Esc` | Discard and close |

Save failures show an error; retry or close without saving.

## Secret Management
- Provider secrets in OS keychain
- Config holds references (`keychain:`, `env:`, `cli:`)
- Harness credentials via harness actions
- `••••••••` by default; `r` to reveal/hide

## Provider Status
| Status | Meaning |
|--------|---------|
| Connected | Authenticated |
| Auth required | No credentials |
| Error | Connection failed |

`t` to test connectivity, `g` to login.

## Testing
Unit: modal confirmation on `Esc` with dirty state, immediate close on clean, save/discard flows, footer hints.

Integration: open settings, edit fields, navigate, test providers, login, verify persistence.
