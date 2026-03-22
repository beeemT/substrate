# Electron App Plan for Substrate

> **Status:** Deferred — revisit after current TUI polish work is complete.

## Executive Summary

Substrate's architecture already separates business logic from presentation cleanly. The domain, service, orchestrator, repository, adapter, and event layers are pure Go with no Bubble Tea coupling. The plan introduces a thin Go API server that exposes these layers over local WebSocket/HTTP, and an Electron app whose renderer replicates the TUI's view hierarchy in React + TypeScript. Both frontends consume the same Go backend; switching between them should feel like switching window managers, not switching products.

---

## 1. Architecture

```
                  ┌──────────────┐    ┌──────────────┐
                  │  Bubble Tea  │    │   Electron   │
                  │  TUI (Go)    │    │   Renderer   │
                  │              │    │  (React/TS)  │
                  └──────┬───────┘    └──────┬───────┘
                         │                   │
                  direct Go calls    WebSocket / HTTP
                         │                   │
                  ┌──────┴───────────────────┴───────┐
                  │         Go API Server             │
                  │   (JSON-RPC over WebSocket +      │
                  │    REST for simple queries +       │
                  │    SSE/WS for event streaming)     │
                  └──────────────┬────────────────────┘
                                 │
              ┌──────────────────┼──────────────────┐
              │                  │                   │
        ┌─────┴─────┐    ┌──────┴──────┐    ┌──────┴──────┐
        │  Service   │    │ Orchestrator│    │   Adapter   │
        │  Layer     │    │   Layer     │    │   Layer     │
        └─────┬─────┘    └──────┬──────┘    └─────────────┘
              │                  │
        ┌─────┴─────┐    ┌──────┴──────┐
        │ Repository│    │  Event Bus  │
        │ (SQLite)  │    │             │
        └───────────┘    └─────────────┘
```

### Key Architectural Decisions

**1. Go backend runs as a local server, not compiled to WASM or FFI.**
Substrate's Go backend manages SQLite, spawns agent subprocesses, shells out to git-work, reads the filesystem, and manages OS keychain secrets. These are fundamentally OS-bound operations. Running Go natively as a sidecar process preserves all of this without bridging pain. The Electron main process spawns the Go binary in `--serve` mode and connects via localhost.

**2. JSON-RPC over WebSocket as the primary IPC protocol.**
The TUI already uses a command/message pattern (Bubble Tea's Elm Architecture). JSON-RPC maps cleanly to this: each `tea.Cmd` in `views/cmds.go` becomes a JSON-RPC method call, each `tea.Msg` in `views/msgs.go` becomes a JSON-RPC response or server-push notification. WebSocket gives us bidirectional streaming needed for real-time log tailing, event subscriptions, and question escalation.

**3. React + TypeScript for the Electron renderer.**
The TUI's view tree is a direct map to a React component tree. React's ecosystem (state management, accessible components, keyboard handling) is battle-tested for Electron apps. TypeScript gives us type safety that mirrors Go's domain types.

**4. Shared design tokens, not shared rendering code.**
The TUI uses Lip Gloss styles and ANSI rendering. The Electron app uses CSS. Attempting to share rendering code between terminal and browser is a dead end. Instead, we share the semantic design language: the same color palette (converted from ANSI to hex), the same spacing ratios, the same status icon vocabulary, the same layout proportions. A `design-tokens.json` file becomes the single source of truth consumed by both `internal/tui/styles/` (Go) and the Electron app's CSS/theme.

**5. The TUI remains the primary interface. Electron is additive.**
The TUI is not deprecated. Both UIs are first-class. The Go API server is new shared infrastructure that the TUI *could* optionally use (for multi-instance coordination) but does not require. The TUI continues to call services directly via Go function calls.

---

## 2. Go API Server Layer

### 2.1 New Package: `internal/server/`

A new package that wraps the existing `Services` struct (currently defined in `internal/tui/views/services.go`) and exposes it over WebSocket + HTTP.

**Refactor:** Move the `Services` struct from `views/services.go` to a shared location (e.g., `internal/app/services.go` or a new `internal/substrate/` package) so both the TUI and the server can import it without the TUI importing server code or vice versa.

```go
// internal/server/server.go
type Server struct {
    svcs    *app.Services  // shared services bundle (refactored out of views/)
    hub     *wsHub         // WebSocket connection manager
    addr    string         // localhost:0 (random port) or configured
}

func (s *Server) Start(ctx context.Context) (port int, err error)
func (s *Server) Shutdown(ctx context.Context) error
```

### 2.2 API Surface

The API mirrors the existing `views/cmds.go` surface almost 1:1. Every command function in `cmds.go` that wraps a service call becomes a JSON-RPC method:

| Current `tea.Cmd` | JSON-RPC Method | Notes |
|---|---|---|
| `LoadSessionsCmd` | `sessions.list` | Filter by workspace |
| `LoadTasksCmd` | `tasks.list` | Filter by workspace |
| `LoadPlanCmd` | `plans.get` | By work item ID |
| `LoadQuestionsCmd` | `questions.list` | Active questions |
| `StartPlanningCmd` | `orchestrator.startPlanning` | Triggers planning pipeline |
| `RunImplementationCmd` | `orchestrator.startImplementation` | Triggers impl pipeline |
| `ApprovePlanCmd` | `plans.approve` | Approve + trigger impl |
| `RejectPlanCmd` | `plans.reject` | Reject plan |
| `RequestChangesCmd` | `plans.requestChanges` | With feedback text |
| `AnswerQuestionCmd` | `questions.answer` | Forward human answer |
| `ResumeTaskCmd` | `orchestrator.resumeTask` | Resume interrupted |
| `AbandonTaskCmd` | `orchestrator.abandonTask` | Mark failed |
| `DeleteSessionCmd` | `sessions.delete` | Full cascade delete |
| `SearchHistoryCmd` | `sessions.searchHistory` | With filters |
| `BrowseWorkItemsCmd` | `adapters.browse` | Unified work browser |
| `CreateManualWorkItemCmd` | `adapters.createManual` | Manual creation |
| ... | ... | Every cmd maps |

### 2.3 Event Streaming

The existing `event.Bus` already supports subscribers. The server registers a subscriber that forwards events to connected WebSocket clients:

```go
// Server subscribes to bus events and pushes to all connected clients
func (s *Server) subscribeEvents() {
    s.svcs.Bus.Subscribe("server", allTopics, func(evt domain.SystemEvent) {
        s.hub.Broadcast(ServerEvent{Type: "system_event", Event: evt})
    })
}
```

Additionally, session log tailing (currently file-based in the TUI via `~/.substrate/sessions/<id>.log`) gets a streaming endpoint:

```
ws://localhost:{port}/stream/session-log/{session-id}
```

This replaces the TUI's file-watch pattern with a push stream that works identically for remote or local clients.

### 2.4 Authentication

Localhost-only. The server binds to `127.0.0.1` and generates a random bearer token on startup, passed to the Electron app via stdout or a temp file. This prevents other local processes from accessing the API without authorization.

### 2.5 Health & Lifecycle

- `GET /health` — readiness probe
- `GET /info` — version, workspace, instance ID
- The Electron main process monitors the Go sidecar and restarts on crash
- Graceful shutdown: Electron sends `SIGTERM`, Go server drains connections and closes DB

---

## 3. Electron App Structure

### 3.1 Project Layout

```
electron/
  package.json
  tsconfig.json
  electron-builder.yml
  src/
    main/                    # Electron main process
      index.ts               # Window creation, Go sidecar lifecycle
      sidecar.ts             # Go binary management (spawn, health, restart)
      ipc.ts                 # Main<->Renderer IPC bridge
    preload/
      index.ts               # Context bridge for renderer security
    renderer/                # React app
      App.tsx                # Root, mirrors views/app.go
      api/
        client.ts            # WebSocket JSON-RPC client
        types.ts             # Generated TypeScript types from Go domain
        events.ts            # Event subscription hooks
        hooks.ts             # React hooks wrapping API calls
      layouts/
        MainLayout.tsx       # Two-pane shell (mirrors app.go View)
        SettingsLayout.tsx   # Full-screen settings
      views/
        Sidebar.tsx          # Mirrors sidebar.go
        Content.tsx          # Mirrors content.go, mode switching
        PlanReview.tsx       # Mirrors plan_review.go
        PlanningView.tsx     # Mirrors planning_view.go
        ImplementingView.tsx # Mirrors implementing_view.go (not yet built)
        ReviewingView.tsx    # Mirrors reviewing_view.go
        CompletedView.tsx    # Mirrors completed_view.go
        InterruptedView.tsx  # Mirrors interrupted_view.go
        QuestionView.tsx     # Mirrors question_view.go
        SessionTranscript.tsx # Mirrors session_transcript.go
        SourceDetails.tsx    # Mirrors source_details_view.go
      overlays/
        NewSession.tsx       # Mirrors overlay_new_session.go (Work Browser)
        SessionSearch.tsx    # Mirrors overlay_session_search.go
        Settings.tsx         # Mirrors settings_page.go
        Help.tsx             # Mirrors overlay_help.go
        WorkspaceInit.tsx    # Mirrors overlay_workspace_init.go
      components/
        OverlayFrame.tsx     # Mirrors components/overlay_frame.go
        Callout.tsx          # Mirrors components/callout.go
        HeaderBlock.tsx      # Mirrors components/header_block.go
        Pane.tsx             # Mirrors components/pane.go
        Tabs.tsx             # Mirrors components/tabs.go
        Toast.tsx            # Mirrors components/toast.go
        Progress.tsx         # Mirrors components/progress.go
        Input.tsx            # Mirrors components/input.go
        Confirm.tsx          # Mirrors components/confirm.go
        StatusBar.tsx        # Mirrors statusbar.go
        KeyHints.tsx         # Mirrors components/keyhints.go
        MarkdownRender.tsx   # Mirrors markdown_render.go
      theme/
        tokens.ts            # Design tokens (from shared design-tokens.json)
        global.css           # Base styles
      state/
        store.ts             # Zustand or similar — mirrors App model state
        types.ts             # App-level state types
```

### 3.2 Main Process

The Electron main process is responsible for:

1. **Go sidecar management:** Resolve the bundled Go binary, spawn it with `--serve --workspace={cwd}`, read the port + auth token from its stdout, monitor health.
2. **Window management:** Single window, titlebar customization, native menu (File, Edit, View, Window, Help).
3. **Auto-update:** Electron Builder's auto-updater for the Electron shell + bundled Go binary.
4. **Deep linking:** `substrate://` URL scheme for opening specific sessions.

```typescript
// src/main/sidecar.ts
class GoSidecar {
  private proc: ChildProcess | null = null;
  private port: number = 0;
  private token: string = '';

  async start(workspaceDir: string): Promise<{port: number, token: string}> {
    const binPath = this.resolveBinary();
    this.proc = spawn(binPath, ['--serve', `--workspace=${workspaceDir}`]);
    // Parse port and token from first stdout line
    const info = await this.readStartupInfo();
    this.port = info.port;
    this.token = info.token;
    return info;
  }

  async healthCheck(): Promise<boolean> { /* GET /health */ }
  async shutdown(): Promise<void> { /* SIGTERM + wait */ }
}
```

### 3.3 Renderer Architecture

**State management:** Zustand (lightweight, hook-based) mirrors the Bubble Tea `App` model. The TUI's `Update` function's message routing maps to Zustand actions dispatched by WebSocket event handlers.

**Data flow (mirrors Bubble Tea Elm Architecture):**
```
User Action -> API Call (JSON-RPC) -> Go Backend processes
                                         |
Go Backend pushes event <- Event Bus
         |
WebSocket event -> Zustand action -> React re-render
```

This is intentionally isomorphic to the TUI's `Update -> Cmd -> Msg -> Update -> View` cycle.

**Keyboard support:** The Electron app preserves all TUI keyboard shortcuts. A `useKeyboard` hook processes keydown events and maps them to the same action vocabulary. Mouse interactions are additive — clicking a sidebar item is equivalent to pressing `j/k` + `Enter`.

---

## 4. Shared Design Language

### 4.1 Design Tokens

Create `design/tokens.json` as the shared source of truth:

```json
{
  "colors": {
    "bg": { "ansi": "default", "hex": "#1a1b26" },
    "fg": { "ansi": "default", "hex": "#c0caf5" },
    "muted": { "ansi": "243", "hex": "#565f89" },
    "accent": { "ansi": "69", "hex": "#7aa2f7" },
    "success": { "ansi": "35", "hex": "#9ece6a" },
    "warning": { "ansi": "214", "hex": "#e0af68" },
    "error": { "ansi": "196", "hex": "#f7768e" },
    "interrupted": { "ansi": "214", "hex": "#e0af68" }
  },
  "status_icons": {
    "running": "●",
    "pending_human": "◐",
    "completed": "✓",
    "interrupted": "⊘",
    "failed": "✗",
    "inactive": "◌"
  },
  "layout": {
    "sidebar_width_chars": 34,
    "sidebar_width_percent": "25%",
    "min_content_width": 60
  }
}
```

Both the Go TUI (`styles/theme.go`) and the Electron renderer (`theme/tokens.ts`) consume this file. The TUI maps `hex` to closest ANSI via termenv; the Electron app uses `hex` directly. Token changes propagate to both frontends.

### 4.2 Visual Parity Principles

1. **Same layout proportions.** Two-pane with sidebar on left, content on right. Settings full-screen. Overlays centered.
2. **Same status vocabulary.** Icons, colors, and labels are identical.
3. **Same information hierarchy.** Sidebar shows the same 3-line entry blocks. Content modes render the same metadata headers, dividers, and body content.
4. **Additive mouse interactions.** Click to select, scroll with trackpad, drag to resize panes, hover for tooltips. These supplement keyboard shortcuts, never replace them.
5. **Same keyboard shortcuts.** `j/k`, `Up/Down`, `n`, `/`, `c`, `?`, `q`, `a`, `r`, `e` — all work identically. The Electron app is keyboard-first with mouse as enhancement.

### 4.3 Where the Electron App Should Differ

The Electron app is not a terminal emulator. It should feel native:

- **Resizable panes** with a draggable divider (the TUI's fixed 34-char sidebar becomes a default that users can drag wider)
- **Text selection and copy** — the TUI can't do this well; the Electron app should
- **Syntax highlighting** in plan review and session transcripts via a proper code editor component (Monaco or CodeMirror)
- **Native scrollbars** instead of viewport-based scroll simulation
- **Rich markdown rendering** in plan review (the TUI uses glamour for approximate rendering; the Electron app can use full markdown-it or similar)
- **Notification integration** — OS-native notifications for question escalation, completion, and failures
- **Multi-window** — potential to open session details in separate windows
- **Better diff rendering** in review mode using a real diff viewer component

---

## 5. Type Sharing Strategy

### 5.1 Go to TypeScript Code Generation

Generate TypeScript types from Go domain structs to prevent drift:

```
internal/domain/*.go  ->  codegen  ->  electron/src/renderer/api/types.ts
```

Tool options:
- **[tygo](https://github.com/gzuidhof/tygo)** — Go struct to TypeScript interface generator. Handles enums, time types, optional fields.
- **Custom generator** — Walk Go AST, emit TS. More control but more maintenance.

Example output:
```typescript
// Generated from internal/domain/plan.go
export interface Plan {
  id: string;
  workItemId: string;
  status: PlanStatus;
  orchestratorPlan: string;
  version: number;
  faq: FAQEntry[];
  createdAt: string; // ISO 8601
  updatedAt: string;
}

export type PlanStatus = 'draft' | 'pending_review' | 'approved' | 'rejected';
```

### 5.2 API Contract

JSON-RPC request/response types are also generated from Go handler signatures. This ensures the Electron client and Go server never disagree on payload shapes.

---

## 6. Implementation Phases

### Phase 0: Shared Infrastructure (1-2 weeks)

**Goal:** Extract the shared services bundle and establish the API server skeleton.

1. **Refactor `Services` struct** out of `internal/tui/views/services.go` into `internal/app/services.go`. Update `views/` to import from the new location. The TUI continues to work identically.
2. **Create `internal/server/`** with WebSocket JSON-RPC server, event streaming, health endpoints. Initially expose 3-5 methods (sessions.list, plans.get, health) to validate the protocol.
3. **Add `--serve` flag** to `cmd/substrate/main.go` that starts the server instead of the TUI.
4. **Create `design/tokens.json`** from current `styles/theme.go` values. Update the Go TUI to read tokens from this file (or generate Go constants from it at build time).
5. **Set up type generation** pipeline: Go structs to TypeScript interfaces.

**Validation:** `substrate --serve` starts, `wscat` can connect and call `sessions.list`, TUI still works with `substrate` (no flag).

### Phase 1: Electron Shell + Core Navigation (2-3 weeks)

**Goal:** Electron app boots, connects to Go sidecar, renders sidebar + empty content.

1. **Scaffold Electron app** with electron-builder, React, TypeScript, Vite.
2. **Implement Go sidecar management** in the main process.
3. **Build WebSocket JSON-RPC client** with reconnection, auth, and typed request/response.
4. **Implement MainLayout** — two-pane with draggable divider.
5. **Implement Sidebar** — session list with status icons, 3-line entries, selection, keyboard navigation.
6. **Implement Content shell** — mode switching based on selected session state.
7. **Implement StatusBar** — workspace context, active session count, key hints.

**Validation:** Launch Electron app, see real sessions from SQLite, navigate with keyboard and mouse, status bar updates.

### Phase 2: Content Modes (Read-Only) (2-3 weeks)

**Goal:** All read-only content modes render correctly.

1. **PlanReview** — Markdown rendering of plan, scroll, section navigation.
2. **PlanningView** — Live log streaming via WebSocket.
3. **ImplementingView** — Repo status row + live log per repo, tab to switch.
4. **ReviewingView** — Critique list, repo tabs, severity styling.
5. **CompletedView** — Summary with repo status and MR/PR links.
6. **InterruptedView** — Interruption details.
7. **QuestionView** — Question display with foreman proposed answer.
8. **SessionTranscript** — Historical transcript rendering with callout cards and thinking blocks.
9. **SourceDetails** — Source metadata for work items.

**Validation:** Every content mode renders with real data. Visual comparison against TUI screenshots for parity.

### Phase 3: Interactive Operations (2-3 weeks)

**Goal:** All user actions work: plan approval, question answering, session creation, etc.

1. **Plan review actions** — Approve, reject, request changes (with feedback input), edit in external editor (open file + watch for changes).
2. **Question answering** — Approve foreman answer, type reply, skip.
3. **New Session overlay (Work Browser)** — Source selection, search, multi-select, start session.
4. **Session Search overlay** — Search input, results list, preview pane, open/delete.
5. **Resume/Abandon** — Interrupted session actions.
6. **Delete** — Work item deletion with confirmation dialog.
7. **Toast notifications** — Success/error/info toasts.
8. **Confirm dialogs** — Reusable confirmation modal.

**Validation:** Complete a full workflow: create session, plan, approve, implement, answer question, review, complete. All from the Electron app.

### Phase 4: Settings + Overlays (1-2 weeks)

**Goal:** Full feature parity with TUI overlays and settings.

1. **Settings page** — Navigation tree + field editor, provider status, secret management (with keychain access via Go API), harness actions (login, auth test).
2. **Help overlay** — Keyboard shortcut reference.
3. **Workspace init modal** — First-start flow.

**Validation:** Configure a new provider, run auth test, change settings — all via Electron.

### Phase 5: Polish + Platform (1-2 weeks)

**Goal:** Production-quality desktop app.

1. **Native notifications** — Question escalation, completion, failure alerts.
2. **Auto-update** — Electron auto-updater for app + bundled Go binary.
3. **Packaging** — macOS .dmg (signed + notarized), Linux .AppImage/.deb, Windows .exe/.msi.
4. **Menu bar** — File, Edit, View, Window, Help with standard accelerators.
5. **Deep links** — `substrate://session/{id}` URL scheme.
6. **Light/dark theme** — System preference detection + manual toggle.
7. **Performance audit** — WebSocket reconnection, memory leaks, large session lists.

**Validation:** Install from .dmg, auto-update works, notifications fire, theme follows system.

---

## 7. Build & Distribution

### 7.1 Packaging the Go Binary

The Electron app bundles a platform-specific Go binary:

```
electron/
  resources/
    bin/
      substrate-darwin-arm64    # macOS Apple Silicon
      substrate-darwin-amd64    # macOS Intel
      substrate-linux-amd64     # Linux
      substrate-win-amd64.exe   # Windows
```

The build pipeline cross-compiles Go for all targets, then electron-builder includes the correct binary per platform.

### 7.2 Version Coupling

The Electron app and Go binary are versioned together. The health endpoint reports the Go binary version; the Electron app checks compatibility on startup. If mismatched (e.g., after partial update), it prompts the user to update.

### 7.3 Development Workflow

```bash
# Terminal 1: Run Go server in dev mode
go run ./cmd/substrate --serve --workspace=$(pwd)

# Terminal 2: Run Electron app in dev mode (hot reload)
cd electron && npm run dev
```

The Electron dev mode connects to a manually-started Go server (configurable via `SUBSTRATE_SERVER_URL`), enabling independent frontend iteration.

---

## 8. Risk Assessment

| Risk | Impact | Mitigation |
|---|---|---|
| IPC latency makes UI feel sluggish | High | Optimistic updates in React; batch API calls; WebSocket keeps connection warm; benchmark early |
| Type drift between Go and TypeScript | Medium | Automated code generation in CI; fail build on drift |
| Go sidecar crashes leave Electron orphaned | Medium | Health check polling; automatic restart with backoff; graceful degradation UI |
| Two UIs to maintain for every feature | High | Shared design tokens; generated types; API contract tests; feature flags to ship TUI-first |
| SQLite concurrent access (TUI + Electron) | Medium | Already handled — Substrate supports multi-instance via `substrate_instances` table + heartbeat. Both clients go through the same Go binary. |
| macOS code signing / notarization | Low | Electron Builder handles this; requires Apple Developer account |
| Binary size (Go + Electron + Node) | Low | Go compresses well; Electron is ~150MB baseline — acceptable for desktop app |

---

## 9. What Does NOT Change

- The TUI (`internal/tui/`) remains untouched except for the `Services` struct extraction
- The domain model, service layer, orchestrator, and adapters are unchanged
- SQLite schema is unchanged
- The bridge (`bridge/omp-bridge.ts`) is unchanged
- Config format is unchanged
- Session log format is unchanged
- The Go binary remains a single binary that can run in TUI mode (default) or serve mode (`--serve`)

---

## 10. Open Questions

1. **Frontend framework:** React is proposed. Svelte or Solid could also work. React has the largest Electron ecosystem (VS Code, Slack, Discord all use it).
2. **State management:** Zustand (lightweight) vs. Redux Toolkit (more structure) vs. Jotai (atomic). Given the Elm Architecture parallel, Zustand's action-based model maps best.
3. **Styling approach:** Tailwind CSS (utility-first, fast iteration) vs. CSS Modules (scoped, explicit) vs. styled-components (JS-in-CSS). Tailwind is proposed for rapid prototyping of the TUI-like aesthetic.
4. **Code editor component:** Monaco (VS Code's editor, heavy but powerful) vs. CodeMirror 6 (lighter, extensible) for plan review editing and diff viewing. CodeMirror 6 is proposed — lighter and sufficient.
5. **Should the TUI eventually also use the API server?** Currently proposed as TUI=direct Go calls, Electron=API server. An alternative is TUI also goes through the server (enables true multi-instance TUI coordination). This is a future consideration, not a Phase 0 requirement.
6. **Naming:** "Substrate Desktop" vs. "Substrate" (with the TUI becoming "Substrate CLI"). Naming affects packaging, docs, and user mental model.
