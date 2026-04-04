# Electron App Plan for Substrate

> **Status:** Ready for implementation.

## Executive Summary

Substrate's architecture already separates business logic from presentation cleanly. The domain, service, orchestrator, repository, adapter, and event layers are pure Go with no Bubble Tea coupling. This plan introduces a thin Go API server that exposes these layers over local WebSocket/HTTP, and an Electron app whose renderer replicates the TUI's view hierarchy in React + TypeScript using shadcn/ui and Aceternity UI for components and motion. Both frontends consume the same Go backend; switching between them should feel like switching window managers, not switching products.

The Electron app is distributed as a **Homebrew Cask** named `substrate` (matching the existing formula in `beeemT/tap`). The project uses **Bun** as its JavaScript runtime and package manager, consistent with the existing `bridge/` package.

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
                  │    REST for simple queries +      │
                  │    SSE/WS for event streaming)    │
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

**3. React + TypeScript + shadcn/ui + Aceternity UI for the Electron renderer.**
The TUI's view tree is a direct map to a React component tree. **shadcn/ui** provides the accessible, composable base component primitives (buttons, dialogs, inputs, tabs, command palette, sheets, toasts) styled via Tailwind CSS. **Aceternity UI** adds the motion and visual polish layer (animated cards, spotlight effects, background gradients, text animations) that elevates the desktop experience beyond what a terminal can achieve. Together they deliver a production-quality UI without building a component library from scratch.

**4. Bun as runtime, bundler, and package manager.**
The project already uses Bun 1.3.9 for the `bridge/` package. The Electron app continues this convention. Bun replaces npm/yarn for installs and scripts, and electron-vite uses Bun as the underlying runtime for the dev server and production builds.

**5. Shared design tokens, not shared rendering code.**
The TUI uses Lip Gloss styles and ANSI rendering. The Electron app uses CSS. Attempting to share rendering code between terminal and browser is a dead end. Instead, we share the semantic design language: the same color palette (the 40 hex tokens from `styles/theme.go`), the same spacing ratios, the same status icon vocabulary, the same layout proportions. A `design/tokens.json` file becomes the single source of truth consumed by both `internal/tui/styles/theme.go` (Go) and the Electron app's Tailwind theme config.

**6. The TUI remains the primary interface. Electron is additive.**
The TUI is not deprecated. Both UIs are first-class. The Go API server is new shared infrastructure that the TUI *could* optionally use (for multi-instance coordination) but does not require. The TUI continues to call services directly via Go function calls.

---

## 2. Go API Server Layer

### 2.1 New Package: `internal/server/`

A new package that wraps the existing service and orchestration layers and exposes them over WebSocket + HTTP.

**Refactor:** The `Services` struct currently lives in `internal/tui/views/services.go`. The TUI-agnostic subset of its fields (services, orchestration, config, bus, adapters, identity) must be extracted into a shared location. `internal/app/` already exists with `wire.go` and `harness.go` — the shared services bundle belongs there. The TUI `views.Services` retains TUI-specific fields (`LogStore`, `LogToasts`, `Settings`, `SettingsData`, `AdapterErrors`, `StartupWarnings`) and embeds or references the shared bundle.

```go
// internal/app/services.go
type Services struct {
    // Core data services
    Session          *service.SessionService
    Plan             *service.PlanService
    Task             *service.TaskService
    Question         *service.QuestionService
    Instance         *service.InstanceService
    Workspace        *service.WorkspaceService
    Review           *service.ReviewService
    Events           *service.EventService
    GithubPRs        *service.GithubPRService
    GitlabMRs        *service.GitlabMRService
    SessionArtifacts *service.SessionReviewArtifactService

    // Orchestration pipelines
    Planning        *orchestrator.PlanningService
    Implementation  *orchestrator.ImplementationService
    ReviewPipeline  *orchestrator.ReviewPipeline
    Resumption      *orchestrator.Resumption
    Foreman         *orchestrator.Foreman
    SessionRegistry *orchestrator.SessionRegistry

    // Runtime
    Cfg         *config.Config
    Adapters    []adapter.WorkItemAdapter
    RepoSources []adapter.RepoSource
    Harnesses   AgentHarnesses
    GitClient   *gitwork.Client
    Bus         *event.Bus

    // Identity
    InstanceID    string
    WorkspaceID   string
    WorkspaceDir  string
    WorkspaceName string
}
```

```go
// internal/server/server.go
type Server struct {
    svcs    *app.Services  // shared services bundle
    hub     *wsHub         // WebSocket connection manager
    addr    string         // localhost:0 (random port) or configured
}

func (s *Server) Start(ctx context.Context) (port int, err error)
func (s *Server) Shutdown(ctx context.Context) error
```

### 2.2 API Surface

The API mirrors the existing `views/cmds.go` surface. Every command function that wraps a service call becomes a JSON-RPC method:

| Current `tea.Cmd` | JSON-RPC Method | Notes |
|---|---|---|
| `LoadSessionsCmd` | `sessions.list` | Filter by workspace |
| `LoadTasksCmd` | `tasks.list` | Filter by workspace |
| `LoadPlanCmd` / `LoadPlanByIDCmd` | `plans.get` | By work item ID or plan ID |
| `LoadQuestionsCmd` | `questions.list` | Active questions |
| `LoadReviewsCmd` | `reviews.list` | By work item ID |
| `StartPlanningCmd` | `orchestrator.startPlanning` | Triggers planning pipeline |
| `RestartPlanningCmd` | `orchestrator.restartPlanning` | Restart with feedback |
| `PlanWithFeedbackCmd` | `orchestrator.planWithFeedback` | Plan correction |
| `RunImplementationCmd` | `orchestrator.startImplementation` | Triggers impl pipeline |
| `SaveReviewedPlanCmd` | `plans.save` | Save edited plan |
| `RejectPlanCmd` | `plans.reject` | Reject plan |
| `AnswerQuestionCmd` | `questions.answer` | Forward human answer |
| `SkipQuestionCmd` | `questions.skip` | Skip question |
| `SendToForemanCmd` | `orchestrator.sendToForeman` | Delegate to foreman |
| `StartForemanCmd` / `StopForemanCmd` | `orchestrator.foremanControl` | Foreman lifecycle |
| `ResumeSessionCmd` | `orchestrator.resumeSession` | Resume interrupted |
| `RetryFailedCmd` | `orchestrator.retryFailed` | Retry failed session |
| `SteerSessionCmd` | `orchestrator.steerSession` | Live steering input |
| `FollowUpSessionCmd` | `orchestrator.followUpSession` | Follow-up on session |
| `FollowUpFailedSessionCmd` | `orchestrator.followUpFailed` | Follow-up failed |
| `FollowUpPlanCmd` | `orchestrator.followUpPlan` | Follow-up plan |
| `DeleteSessionCmd` | `sessions.delete` | Full cascade delete |
| `SearchSessionHistoryCmd` | `sessions.searchHistory` | With filters |
| `LoadSessionInteractionCmd` | `sessions.getInteraction` | Session transcript |
| `HeartbeatCmd` | `instances.heartbeat` | Instance keepalive |
| `LoadLiveInstancesCmd` | `instances.listLive` | Active instances |
| `WorkspaceHealthCheckCmd` | `workspace.healthCheck` | Workspace status |
| `LoadReposCmd` | `repos.list` | Available repositories |
| `CloneRepoCmd` | `repos.clone` | Clone a repository |
| `OpenBrowserCmd` | `system.openURL` | Open URL in browser |
| Adapter browse (overlay_new_session) | `adapters.browse` | Unified work browser |
| Manual creation (overlay_new_session) | `adapters.createManual` | Manual work item |
| Settings snapshot | `settings.get` | Current settings |
| Settings apply | `settings.apply` | Apply settings changes |
| Settings test provider | `settings.testProvider` | Provider auth test |
| Settings login | `settings.login` | Provider login flow |

### 2.3 Event Streaming

The existing `event.Bus` already supports subscribers. The server registers a subscriber that forwards events to connected WebSocket clients:

```go
func (s *Server) subscribeEvents() {
    s.svcs.Bus.Subscribe("server", allTopics, func(evt domain.SystemEvent) {
        s.hub.Broadcast(ServerEvent{Type: "system_event", Event: evt})
    })
}
```

Session log tailing (currently file-based in the TUI via `~/.substrate/sessions/<id>.log`) gets a streaming endpoint:

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
desktop/
  package.json               # Bun workspace, Electron deps
  bunfig.toml                # Bun config
  biome.json                 # Linting (matches bridge/ convention)
  tsconfig.json
  electron-builder.yml
  components.json            # shadcn/ui config
  tailwind.config.ts         # Tailwind + design token integration
  postcss.config.js
  src/
    main/                    # Electron main process
      index.ts               # Window creation, Go sidecar lifecycle
      sidecar.ts             # Go binary management (spawn, health, restart)
      ipc.ts                 # Main<->Renderer IPC bridge
    preload/
      index.ts               # Context bridge for renderer security
    renderer/                # React app
      App.tsx                # Root, mirrors views/app.go
      index.html
      index.css              # Tailwind directives + Aceternity globals
      api/
        client.ts            # WebSocket JSON-RPC client
        types.ts             # Generated TypeScript types from Go domain
        events.ts            # Event subscription hooks
        hooks.ts             # React hooks wrapping API calls
      layouts/
        MainLayout.tsx       # Two-pane shell (mirrors app.go View)
        SettingsLayout.tsx   # Full-screen settings
      views/
        Sidebar.tsx          # Mirrors sidebar.go — session list, filters, grouping
        Content.tsx          # Mirrors content.go — mode switching
        Overview.tsx         # Mirrors overview.go — root session overview
        PlanReview.tsx       # Mirrors plan_review.go — markdown + approve/reject/feedback
        PlanningView.tsx     # Mirrors planning_view.go — live log streaming + spinner
        QuestionView.tsx     # Mirrors question_view.go — approve/send/skip
        InterruptedView.tsx  # Mirrors interrupted_view.go — resume/restart/abandon
        CompletedView.tsx    # Mirrors completed_view.go — MR/PR links + follow-up
        ReviewingView.tsx    # Mirrors reviewing_view.go — critique list + repo tabs
        SourceDetails.tsx    # Mirrors source_details_view.go — metadata pane
        SessionTranscript.tsx # Mirrors session_transcript.go — block rendering
      overlays/
        NewSession.tsx       # Mirrors overlay_new_session.go — work browser + manual
        SessionSearch.tsx    # Mirrors overlay_session_search.go — search + preview
        SourceItems.tsx      # Mirrors overlay_source_items.go — split-pane items
        AddRepo.tsx          # Mirrors overlay_add_repo.go — repo browser/clone
        Settings.tsx         # Mirrors settings_page.go — section/field editor
        Help.tsx             # Mirrors overlay_help.go — keybind cheat sheet
        Logs.tsx             # Mirrors overlay_logs.go — scrollable slog entries
        WorkspaceInit.tsx    # Mirrors overlay_workspace_init.go — first-start flow
      dialogs/
        DuplicateSession.tsx # Mirrors duplicate_session_dialog.go
        Confirm.tsx          # Mirrors components/confirm.go — reusable confirm
      components/
        # shadcn/ui primitives (installed via `bunx shadcn@latest add`)
        ui/
          button.tsx
          dialog.tsx
          input.tsx
          textarea.tsx
          tabs.tsx
          command.tsx         # Command palette (session search, work browser)
          sheet.tsx           # Overlay/drawer surfaces
          toast.tsx           # Toast notifications via sonner
          badge.tsx           # Status badges
          scroll-area.tsx
          separator.tsx
          tooltip.tsx
          dropdown-menu.tsx
          progress.tsx
          skeleton.tsx
          card.tsx
          popover.tsx
        # Aceternity UI components (copied + adapted)
        aceternity/
          spotlight.tsx       # Spotlight hover effect on cards
          background-gradient.tsx  # Animated gradient backgrounds
          text-generate-effect.tsx # Text reveal animations
          moving-border.tsx   # Animated border for active sessions
          bento-grid.tsx      # Grid layout for dashboard/overview
          floating-dock.tsx   # Quick-action dock
          sidebar.tsx         # Animated sidebar with Aceternity motion
        # App-specific composites
        HeaderBlock.tsx      # Mirrors components/header_block.go
        Pane.tsx             # Mirrors components/pane.go — bordered panel
        Callout.tsx          # Mirrors components/callout.go — status cards
        KeyHints.tsx         # Mirrors components/keyhints.go — shortcut hints
        StatusBar.tsx        # Mirrors statusbar.go — footer with hints + metadata
        ProgressBar.tsx      # Mirrors components/progress.go
        MarkdownRender.tsx   # Rich markdown via react-markdown + rehype
        SessionEntry.tsx     # Sidebar entry block (3-line session card)
        BunnyIdle.tsx        # Mirrors components/bunny.go — animated empty state
      theme/
        tokens.ts            # Design tokens (from shared design/tokens.json)
        colors.ts            # Semantic color map matching theme.go palette
      state/
        store.ts             # Zustand store — mirrors App model state
        types.ts             # App-level state types
        selectors.ts         # Derived state selectors
      hooks/
        useKeyboard.ts       # Global keyboard shortcut handler
        useWebSocket.ts      # WebSocket connection + reconnection
        useSessionLog.ts     # Live log streaming hook
        useTheme.ts          # System theme detection + manual toggle
      lib/
        utils.ts             # cn() helper, formatting utilities
```

### 3.2 Main Process

The Electron main process is responsible for:

1. **Go sidecar management:** Resolve the bundled Go binary, spawn it with `--serve --workspace={cwd}`, read the port + auth token from its stdout, monitor health.
2. **Window management:** Single window, titlebar customization, native menu (File, Edit, View, Window, Help).
3. **Auto-update:** Electron auto-updater for the Electron shell + bundled Go binary.
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

**Component architecture:**

- **shadcn/ui** provides the primitive layer: buttons, inputs, dialogs, command palette, sheets, toasts (via Sonner), scroll areas, tooltips, dropdowns. These are installed as source files (not a dependency) via `bunx shadcn@latest add`, giving full control over styling and behavior.
- **Aceternity UI** provides the motion layer: spotlight effects on session cards, animated borders on active sessions, background gradients for the idle state, text generation effects for plan streaming, bento grid for overview layouts. These are copied and adapted (Aceternity is a copy-paste component library, not an npm package).
- **App composites** combine primitives with domain logic: `SessionEntry` uses a shadcn Card with Aceternity's moving-border for active sessions; `Callout` uses a shadcn Card with variant-specific Aceternity spotlight effects; the sidebar uses Aceternity's animated sidebar component with shadcn's scroll-area and tooltip.

**Keyboard support:** The Electron app preserves all TUI keyboard shortcuts. A `useKeyboard` hook processes keydown events and maps them to the same action vocabulary. Mouse interactions are additive — clicking a sidebar item is equivalent to pressing `j/k` + `Enter`.

---

## 4. Shared Design Language

### 4.1 Design Tokens

Create `design/tokens.json` as the shared source of truth, derived from the current `styles/theme.go` `DefaultTheme`:

```json
{
  "colors": {
    "headerBg": { "ansi": null, "hex": "#1a1a2e" },
    "headerFg": { "ansi": null, "hex": "#e0e0e0" },
    "statusBarBg": { "ansi": null, "hex": "#16213e" },
    "statusBarFg": { "ansi": null, "hex": "#a0a0a0" },
    "keybindAccent": { "ansi": null, "hex": "#5b8def" },
    "pending": { "ansi": null, "hex": "#6b7280" },
    "active": { "ansi": null, "hex": "#5b8def" },
    "success": { "ansi": null, "hex": "#34d399" },
    "error": { "ansi": null, "hex": "#f87171" },
    "warning": { "ansi": null, "hex": "#fbbf24" },
    "interrupted": { "ansi": null, "hex": "#f59e0b" },
    "title": { "ansi": null, "hex": "#f0f0f0" },
    "subtitle": { "ansi": null, "hex": "#b0b0b0" },
    "muted": { "ansi": null, "hex": "#6b7280" },
    "hint": { "ansi": null, "hex": "#6b7280" },
    "label": { "ansi": null, "hex": "#94a3b8" },
    "accent": { "ansi": null, "hex": "#5b8def" },
    "link": { "ansi": null, "hex": "#7dd3fc" },
    "divider": { "ansi": null, "hex": "#2d2d44" },
    "thinking": { "ansi": null, "hex": "#8899a6" },
    "border": { "ansi": null, "hex": "#2d2d44" },
    "paneBorder": { "ansi": null, "hex": "#334155" },
    "paneBorderFocused": { "ansi": null, "hex": "#60a5fa" },
    "toolBorder": { "ansi": null, "hex": "#475569" },
    "overlayBorder": { "ansi": null, "hex": "#2d2d44" },
    "overlayBorderFocused": { "ansi": null, "hex": "#60a5fa" },
    "selectedBg": { "ansi": null, "hex": "#1e293b" },
    "selectionActive": { "ansi": null, "hex": "#1e293b" },
    "selectionInactive": { "ansi": null, "hex": "#122033" },
    "settingsText": { "ansi": null, "hex": "#cbd5e1" },
    "settingsTextStrong": { "ansi": null, "hex": "#f8fafc" },
    "settingsBreadcrumb": { "ansi": null, "hex": "#93c5fd" },
    "scrollbarTrack": { "ansi": null, "hex": "#64748b" },
    "scrollbarThumb": { "ansi": null, "hex": "#cbd5e1" },
    "scrollbarThumbFocused": { "ansi": null, "hex": "#60a5fa" },
    "diffAdd": { "ansi": null, "hex": "#34d399" },
    "diffDel": { "ansi": null, "hex": "#f87171" },
    "codeBlockBg": { "ansi": null, "hex": "#0f0f1a" },
    "toolCallBg": { "ansi": null, "hex": "#0d0d14" }
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
    "min_content_width": 60,
    "min_width_for_pane_gap": 60
  }
}
```

Both the Go TUI (`styles/theme.go`) and the Electron renderer (`theme/tokens.ts`) consume this file. Token changes propagate to both frontends. The Tailwind config extends its theme from these tokens.

### 4.2 Visual Parity Principles

1. **Same layout proportions.** Two-pane with sidebar on left, content on right. Settings full-screen. Overlays centered (using shadcn Dialog/Sheet).
2. **Same status vocabulary.** Icons, colors, and labels are identical.
3. **Same information hierarchy.** Sidebar shows the same multi-line entry blocks (as Aceternity spotlight cards). Content modes render the same metadata headers, dividers, and body content.
4. **Additive mouse interactions.** Click to select, scroll with trackpad, drag to resize panes, hover for tooltips and Aceternity spotlight effects. These supplement keyboard shortcuts, never replace them.
5. **Same keyboard shortcuts.** `j/k`, `Up/Down`, `n`, `/`, `c`, `?`, `q`, `a`, `r`, `e` — all work identically. The Electron app is keyboard-first with mouse as enhancement.
6. **Same sidebar filters and dimensions.** All/Active/NeedsAttention/Completed filters and None/State/Source/Created/Activity grouping dimensions carry over.

### 4.3 Where the Electron App Should Differ

The Electron app is not a terminal emulator. It should feel native and use Aceternity's motion library to create a polished desktop experience:

- **Resizable panes** with a draggable divider (the TUI's fixed 34-char sidebar becomes a default that users can drag wider)
- **Text selection and copy** — the TUI can't do this well; the Electron app should
- **Syntax highlighting** in plan review and session transcripts via CodeMirror 6 (lighter than Monaco, sufficient for read + light edit)
- **Rich markdown rendering** in plan review via react-markdown + rehype plugins (the TUI uses glamour for approximate rendering)
- **Animated transitions** — Aceternity's text-generate-effect for streaming plan content, moving-border for active sessions, spotlight on hover, background-gradient for the idle/empty state (replacing the TUI's ASCII bunny with a visually rich idle screen)
- **Native scrollbars** via shadcn's ScrollArea instead of viewport-based scroll simulation
- **Notification integration** — OS-native notifications for question escalation, completion, and failures
- **Command palette** — shadcn's Command component (cmdk-based) for quick session switching, action execution, and search (maps to the TUI's `/` search and `n` new session)
- **Better diff rendering** in review mode using a real diff viewer component
- **Multi-window** — potential to open session details in separate windows
- **Native scrollbars** instead of viewport-based scroll simulation
- **Toast notifications** via Sonner (shadcn's toast primitive) matching the TUI's toast stack behavior

---

## 5. Type Sharing Strategy

### 5.1 Go to TypeScript Code Generation

Generate TypeScript types from Go domain structs to prevent drift:

```
internal/domain/*.go  ->  codegen  ->  desktop/src/renderer/api/types.ts
```

Tool: **[tygo](https://github.com/gzuidhof/tygo)** — Go struct to TypeScript interface generator. Handles enums, time types, optional fields. Run as a `bun run generate:types` script that invokes the Go tool.

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

// Generated from internal/domain/session.go
export interface Session {
  id: string;
  workspaceId: string;
  title: string;
  status: SessionStatus;
  source: string;
  sourceRef: string;
  // ...
}

// Generated from internal/domain/question.go
export interface Question {
  id: string;
  sessionId: string;
  question: string;
  proposedAnswer: string;
  context: string;
  status: QuestionStatus;
  answer: string;
}
```

### 5.2 API Contract

JSON-RPC request/response types are also generated from Go handler signatures. This ensures the Electron client and Go server never disagree on payload shapes.

---

## 6. Implementation Phases

### Phase 0: Shared Infrastructure (1-2 weeks)

**Goal:** Extract the shared services bundle and establish the API server skeleton.

1. **Extract shared `app.Services` struct** from `internal/tui/views/services.go` into `internal/app/services.go`. The `internal/app/` package already exists with `wire.go` (adapter construction) and `harness.go` (`AgentHarnesses`). Move the service bundle there. Update `views.Services` to embed or reference `app.Services` plus TUI-specific fields (`LogStore`, `LogToasts`, `Settings`, `SettingsData`, `AdapterErrors`, `StartupWarnings`). The TUI continues to work identically.
2. **Create `internal/server/`** with WebSocket JSON-RPC server, event streaming, health endpoints. Initially expose 5 methods (`sessions.list`, `tasks.list`, `plans.get`, `questions.list`, `/health`) to validate the protocol.
3. **Add `--serve` flag** to `cmd/substrate/main.go` that starts the server instead of the TUI. The `run()` function already builds all layers before calling `views.RunTUI()` — branch at that point to call `server.Start()` instead.
4. **Create `design/tokens.json`** from current `styles/theme.go` values (the 40 hex color tokens, status icons, layout metrics). Optionally generate Go constants from it at build time so `theme.go` stays in sync.
5. **Set up type generation** pipeline: Go domain structs to TypeScript interfaces via tygo.

**Validation:** `substrate --serve` starts, `wscat` can connect and call `sessions.list`, TUI still works with `substrate` (no flag).

### Phase 1: Electron Shell + Core Navigation (2-3 weeks)

**Goal:** Electron app boots, connects to Go sidecar, renders sidebar + content shell.

1. **Scaffold Electron app** in `desktop/` with electron-vite, React, TypeScript, Bun, Tailwind CSS, Biome (matching `bridge/` conventions).
2. **Install shadcn/ui** via `bunx shadcn@latest init` — configure with Tailwind, design tokens, and dark theme defaults.
3. **Copy Aceternity UI components** needed for Phase 1: `sidebar.tsx`, `spotlight.tsx`, `moving-border.tsx`, `background-gradient.tsx`.
4. **Implement Go sidecar management** in the main process (`sidecar.ts`).
5. **Build WebSocket JSON-RPC client** with reconnection, auth, and typed request/response.
6. **Implement MainLayout** — two-pane with draggable divider, Aceternity animated sidebar.
7. **Implement Sidebar** — session list with status icons, multi-line entries (as spotlight cards), keyboard navigation, filter/grouping controls.
8. **Implement Content shell** — mode switching based on selected session state, empty state with Aceternity background-gradient.
9. **Implement StatusBar** — workspace context, active session count, key hints.
10. **Set up Zustand store** — initial state types mirroring App model (sessions, selectedSession, contentMode, overlayState, sidebar filter/dimension).

**Validation:** Launch Electron app, see real sessions from SQLite, navigate with keyboard and mouse, status bar updates.

### Phase 2: Content Modes (Read-Only) (2-3 weeks)

**Goal:** All read-only content modes render correctly.

1. **Overview** — Session metadata header, status callouts, action buttons (shadcn Buttons + Cards).
2. **PlanReview** — Rich markdown rendering via react-markdown + rehype, scroll, section navigation, Aceternity text-generate-effect for streaming.
3. **PlanningView** — Live log streaming via WebSocket, spinner, CodeMirror for log content.
4. **ReviewingView** — Critique list with severity badges (shadcn Badge), repo tabs (shadcn Tabs).
5. **CompletedView** — Summary with repo status, MR/PR links (shadcn Cards + Links).
6. **InterruptedView** — Interruption details with resume/abandon actions.
7. **QuestionView** — Question display with foreman proposed answer, approve/send/skip buttons.
8. **SessionTranscript** — Historical transcript rendering with callout cards, thinking blocks, tool call grouping.
9. **SourceDetails** — Source metadata pane with notice callout.

**Validation:** Every content mode renders with real data. Visual comparison against TUI screenshots for parity.

### Phase 3: Interactive Operations (2-3 weeks)

**Goal:** All user actions work: plan approval, question answering, session creation, steering, follow-up.

1. **Plan review actions** — Approve, reject, request changes (with feedback textarea), edit in external editor (open file + watch for changes).
2. **Question answering** — Approve foreman answer, type reply, skip.
3. **Session steering** — Live input to running sessions via `SteerSessionCmd`.
4. **Follow-up actions** — Follow-up on completed/failed sessions, follow-up on plans.
5. **New Session overlay** — shadcn Command palette for work browser, adapter-backed browsing with provider/scope/view/state cycling, manual creation.
6. **Session Search overlay** — Command palette with debounced search, result list, preview pane, workspace/global scope toggle.
7. **Source Items overlay** — Split-pane with list selection and detail, multi-select URL opening.
8. **Add Repo overlay** — Repository browser/clone with search and manual URL entry.
9. **Resume/Abandon** — Interrupted session actions with restart-planning option.
10. **Delete** — Work item deletion with shadcn AlertDialog confirmation.
11. **Duplicate session dialog** — Cancel, open-existing, create-session options.
12. **Toast notifications** — Sonner-based toasts matching TUI toast levels (Info/Success/Warning/Error).

**Validation:** Complete a full workflow: create session, plan, approve, implement, answer question, review, complete. All from the Electron app.

### Phase 4: Settings + Overlays (1-2 weeks)

**Goal:** Full feature parity with TUI overlays and settings.

1. **Settings page** — Full-screen layout with section navigation tree (shadcn Accordion/Collapsible), field editor, provider status badges, secret management (with keychain access via Go API), harness actions (login, auth test).
2. **Help overlay** — Keyboard shortcut reference in shadcn Dialog.
3. **Logs overlay** — Scrollable slog entries with clipboard copy, level filtering.
4. **Workspace init modal** — First-start flow with workspace scan and initialization.

**Validation:** Configure a new provider, run auth test, change settings — all via Electron.

### Phase 5: Polish + Platform (1-2 weeks)

**Goal:** Production-quality desktop app.

1. **Native notifications** — Question escalation, completion, failure alerts.
2. **Auto-update** — Electron auto-updater for app + bundled Go binary.
3. **Homebrew Cask** — `substrate` cask in `beeemT/tap` (see Section 7.3).
4. **Packaging** — macOS .dmg (signed + notarized), Linux .AppImage/.deb.
5. **Menu bar** — File, Edit, View, Window, Help with standard accelerators.
6. **Deep links** — `substrate://session/{id}` URL scheme.
7. **Light/dark theme** — System preference detection + manual toggle. Dark is default (matches TUI palette).
8. **Command palette** — Global `Cmd+K` / `Ctrl+K` quick-action palette via shadcn Command.
9. **Performance audit** — WebSocket reconnection, memory leaks, large session lists, Aceternity animation perf.

**Validation:** Install from Cask, auto-update works, notifications fire, theme follows system.

---

## 7. Build & Distribution

### 7.1 Packaging the Go Binary

The Electron app bundles a platform-specific Go binary:

```
desktop/
  resources/
    bin/
      substrate-darwin-arm64    # macOS Apple Silicon
      substrate-darwin-amd64    # macOS Intel
      substrate-linux-amd64     # Linux
      substrate-linux-arm64     # Linux ARM
```

The build pipeline cross-compiles Go for all targets, then electron-builder includes the correct binary per platform.

### 7.2 Version Coupling

The Electron app and Go binary are versioned together. The health endpoint reports the Go binary version; the Electron app checks compatibility on startup. If mismatched (e.g., after partial update), it prompts the user to update.

### 7.3 Homebrew Distribution

The existing Homebrew formula `Substrate` in `beeemT/tap` at `Formula/substrate.rb` installs the CLI/TUI. The Electron app is distributed as a **Cask** with the same name:

```ruby
# Casks/substrate.rb in beeemT/tap
cask "substrate" do
  version "0.1.0"
  sha256 "..."

  url "https://github.com/beeemT/substrate/releases/download/v#{version}/Substrate-#{version}-arm64.dmg"
  name "Substrate"
  desc "AI-powered work item orchestration — desktop app"
  homepage "https://github.com/beeemT/substrate"

  app "Substrate.app"

  zap trash: [
    "~/Library/Application Support/Substrate",
    "~/Library/Preferences/com.beeemT.substrate.plist",
  ]
end
```

Homebrew allows a formula and a cask to share the same name. Users install the TUI with `brew install beeemT/tap/substrate` and the desktop app with `brew install --cask beeemT/tap/substrate`. The release workflow generates both.

### 7.4 Development Workflow

```bash
# Terminal 1: Run Go server in dev mode
go run ./cmd/substrate --serve --workspace=$(pwd)

# Terminal 2: Run Electron app in dev mode (hot reload)
cd desktop && bun run dev
```

The Electron dev mode connects to a manually-started Go server (configurable via `SUBSTRATE_SERVER_URL`), enabling independent frontend iteration.

For the full stack:
```bash
# Build everything and run
cd desktop && bun run build && bun run start
```

---

## 8. Tech Stack Summary

| Layer | Technology | Rationale |
|---|---|---|
| Runtime / Package Manager | **Bun** | Already used for `bridge/`; fast installs, native TS execution |
| Electron Build | **electron-vite** | Vite-based Electron build tooling, supports Bun |
| UI Framework | **React 19 + TypeScript** | Largest Electron ecosystem; component tree maps to TUI views |
| Component Primitives | **shadcn/ui** | Accessible, composable, source-owned; Tailwind-native |
| Motion / Polish | **Aceternity UI** | Copy-paste animated components; spotlight, borders, gradients |
| Styling | **Tailwind CSS v4** | Utility-first; design tokens integrate via theme config |
| State Management | **Zustand** | Lightweight, action-based; maps to Elm Architecture pattern |
| Linting / Formatting | **Biome** | Matches `bridge/` convention; single tool for lint + format |
| Markdown Rendering | **react-markdown + rehype** | Rich rendering for plans and transcripts |
| Code Editor | **CodeMirror 6** | Lighter than Monaco; sufficient for plan edit + diff view |
| Type Generation | **tygo** | Go struct to TypeScript interface generator |
| Packaging | **electron-builder** | Mature; handles macOS signing, notarization, auto-update |

---

## 9. Risk Assessment

| Risk | Impact | Mitigation |
|---|---|---|
| IPC latency makes UI feel sluggish | High | Optimistic updates in Zustand; batch API calls; WebSocket keeps connection warm; benchmark early |
| Type drift between Go and TypeScript | Medium | Automated tygo generation in CI; fail build on drift |
| Go sidecar crashes leave Electron orphaned | Medium | Health check polling; automatic restart with backoff; graceful degradation UI |
| Two UIs to maintain for every feature | High | Shared design tokens; generated types; API contract tests; feature flags to ship TUI-first |
| SQLite concurrent access (TUI + Electron) | Medium | Already handled — Substrate supports multi-instance via `substrate_instances` table + heartbeat. Both clients go through the same Go binary. |
| macOS code signing / notarization | Low | Electron Builder handles this; requires Apple Developer account |
| Binary size (Go + Electron + Node) | Low | Go compresses well; Electron is ~150MB baseline — acceptable for desktop app |
| Aceternity component maintenance | Low | Components are source-owned (copied); no external dependency to break. Updates are manual and deliberate. |
| shadcn breaking changes | Low | Source-owned components (not a package dependency); Tailwind theme pinned |

---

## 10. What Does NOT Change

- The TUI (`internal/tui/`) remains untouched except for the `Services` struct extraction
- The domain model, service layer, orchestrator, and adapters are unchanged
- SQLite schema is unchanged
- The bridge (`bridge/omp-bridge.ts`, `bridge/claude-agent-bridge.ts`) is unchanged
- Config format is unchanged
- Session log format is unchanged
- The Go binary remains a single binary that can run in TUI mode (default) or serve mode (`--serve`)
- The Homebrew formula for the CLI/TUI is unchanged

---

## 11. Resolved Decisions

These questions from the prior plan revision are now settled:

1. **Frontend framework:** React — largest Electron ecosystem, maps cleanly to TUI component tree.
2. **State management:** Zustand — lightweight, action-based model maps best to the Elm Architecture parallel.
3. **Styling approach:** Tailwind CSS via shadcn/ui + Aceternity UI for components and motion.
4. **Code editor component:** CodeMirror 6 — lighter than Monaco, sufficient for plan review editing and diff viewing.
5. **Package manager / runtime:** Bun — consistent with existing `bridge/` infrastructure.
6. **Naming:** The Homebrew Cask is `substrate` (same name as the formula). `brew install substrate` gives you the CLI; `brew install --cask substrate` gives you the desktop app.
7. **Should the TUI eventually also use the API server?** Not in initial scope. The TUI continues direct Go calls. The API server is Electron-only for now. Future consideration for multi-instance TUI coordination.
