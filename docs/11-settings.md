# Settings System

<!-- docs:last-integrated-commit 10e50295fb75f72c67233e191ae34fb8fc091f1e -->

Full-screen configuration covering commit strategy, planning, review, Foreman, harness routing, provider auth, and repository lifecycle.

## Page Structure
```
┌─ Settings ───────────────┬─────────────────────────────────────────┐
│ ▼ Home View               │ Home View                                │
│ ▼ Commit                  │ ────────────────────────────────────────│
│ ▼ Planning                │ Documentation files the planning agent   │
│ ▼ Review                  │ reads before writing a plan.           │
│ ▼ Harness Routing         │                                          │
│ ▼ Provider · GitHub       │ Default: ./docs, ./SPEC.md               │
│ ▼ Provider · Linear       │ Current: ./docs, ./ARCHITECTURE.md       │
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
| `↑↓` or `jk` | Navigate sections |
| `→` / `l` | Expand / focus fields |
| `←` / `h` | Collapse / parent |
| `Enter` | Focus fields |
| `Esc` | Close (confirms if dirty) |

### Field Focus
| Key | Action |
|-----|--------|
| `↑↓` or `jk` | Navigate fields |
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
