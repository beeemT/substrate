# Electron App Plan for Substrate

<!-- docs:last-integrated-commit 10e50295fb75f72c67233e191ae34fb8fc091f1e -->

## Executive Summary

Substrate separates business logic from presentation. The domain, service, orchestrator, repository, adapter, and event layers are pure Go with no terminal coupling. This plan introduces a Go API server exposing these layers over WebSocket and HTTP, and an Electron app whose renderer mirrors the TUI's view hierarchy in React. Both frontends consume the same Go backend — switching between them feels like switching window managers, not switching products.

Distributed as a Homebrew Cask named `substrate`, using Bun as the JavaScript runtime, consistent with the existing bridge package.

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
                  │   (JSON-RPC + REST + WS stream)   │
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

**1. Go backend runs as a local server, not WASM or FFI.**
Substrate manages SQLite, spawns agent subprocesses, shells out to git-work, reads the filesystem, and accesses OS keychain secrets — all OS-bound operations. Running Go natively as a sidecar preserves everything. The Electron main process spawns the Go binary in serve mode and connects via localhost.

**2. JSON-RPC over WebSocket as the primary IPC protocol.**
The TUI maps each user intent to a service call. JSON-RPC preserves this cleanly: calls become remote methods, pushes become server-side events. WebSocket gives bidirectional streaming for real-time log tailing, event subscriptions, and question escalation without polling.

**3. React + TypeScript with accessible component primitives and a motion layer.**
The TUI's view tree maps directly to a React component tree. Base primitives (dialogs, inputs, tabs, command palette, sheets, toasts, scroll areas, tooltips) come from a component library. A motion layer adds visual polish (animated cards, spotlight effects, gradients, text animations) that elevates the desktop experience without building from scratch.

**4. Bun as runtime, bundler, and package manager.**
Consistent with the existing bridge package. Bun replaces npm/yarn and the build tooling uses it as the underlying runtime.

**5. Shared design tokens, not shared rendering code.**
The TUI uses terminal styling; the Electron app uses CSS. Sharing rendering code is a dead end. Share the semantic design language instead: color palette, spacing ratios, status icon vocabulary, and layout proportions. A single token file is the source of truth for both frontends.

**6. The TUI remains the primary interface. Electron is additive.**
The TUI is not deprecated. Both UIs are first-class. The API server is shared infrastructure the TUI could optionally use for coordination but does not require.

---

## 2. Go API Server Layer

A new package wraps the existing service and orchestration layers and exposes them over WebSocket and HTTP, mirroring the full TUI command surface: session management (list, create, resume, delete, search), orchestration pipelines (planning, implementation, review), question handling (answer, skip, delegate), workspace and repository operations, and settings. A shared services bundle extracted from the TUI views serves both frontends.

**Event streaming:** The existing event bus forwards events to WebSocket clients — session lifecycle, question arrivals, plan status, review cycles, and autonomous mode notifications arrive identically in both frontends. Session log tailing also gets a streaming endpoint, so remote clients work without filesystem access.

**Authentication:** Binds to localhost only with a random bearer token passed via stdout, preventing unauthorized local access.

**Health and lifecycle:** Readiness and info endpoints report health and version. The Electron main process monitors the sidecar and restarts on crash. Graceful shutdown drains connections and closes the database cleanly.

---

## 3. Electron App Structure

The React renderer mirrors the TUI's view hierarchy: two-pane shell with sidebar and content, overlays for modal workflows, status bar, and toasts. State management uses an action-based store isomorphic to the TUI's update loop.

**Component layers:**
- **Primitives** — accessible base components (dialogs, inputs, tabs, command palette, sheets, scroll areas, tooltips) via utility CSS
- **Motion components** — animated elements (spotlight hover, animated borders for active sessions, idle-state gradients, text reveal, bento grid)
- **Domain composites** — session cards, plan viewers, question responders, artifact accordions combining primitives and motion with business logic

Mouse interactions are additive: clicking a sidebar item selects it, clicking an action button triggers the same intent as its keyboard equivalent. The full keyboard-driven workflow is preserved.

**Main process:** Manages Go sidecar lifecycle (spawn, health monitor, restart on crash), window management (single window, native menu), auto-update for both the Electron shell and bundled Go binary, and deep links via URL scheme.

---

## 4. Shared Design Language

A single token file is the source of truth for both frontends: color palette (header, status, accent, muted, border, selection, diff), status icon vocabulary (running, pending human, completed, interrupted, failed, inactive), and layout metrics (sidebar proportions, minimum content width, pane gap). Changes propagate to both frontends simultaneously.

**Parity requirement:** The Electron app replicates the TUI exactly. Every pane, overlay, content mode, sidebar entry, keyboard binding, filter, grouping dimension, and flow sequence must exist. Mouse and visual enhancements (animations, resizable panes, rich markdown) are additive — they never replace a keyboard flow or omit information the TUI shows.

### Shell Layout

```
┌──────────────────────────────────────────────────────┐
│  ┌────────────┐ ┌──────────────────────────────────┐  │
│  │  Sidebar    │ │  Content                         │  │
│  │  (fixed     │ │  (flexible width, mode-switched) │  │
│  │   width)    │ │                                  │  │
│  │             │ │                                  │  │
│  └────────────┘ └──────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────┐  │
│  │  Status Bar (1-2 rows)                           │  │
│  └──────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────┘
        ┌──────────┐
        │ Toasts   │  (top-right, stacked)
        └──────────┘
```

Two-pane shell with resizable sidebar divider. Focus changes only the pane border color — layout does not shift. Overlay order: workspace-init → confirm → duplicate-session → active overlay → toasts. Toasts: top-right, max 30% window width, 20s auto-dismiss, newest-first ordering.

---

## 5. Type Sharing

TypeScript types are generated from Go domain structs via an automated build step (CI-enforced). Changes to the domain model propagate to the frontend without manual synchronization.

---

## 6. Build and Distribution

The Electron app bundles a platform-specific Go binary. The build pipeline cross-compiles Go for all targets (macOS arm64/amd64, Linux amd64/arm64) and includes the correct binary per platform. App and Go binary are versioned together; the health endpoint reports the Go version and the app checks compatibility on startup.

Distributed as a Homebrew Cask named `substrate` in `beeemT/tap`. Homebrew allows a formula and cask to share the same name — CLI via `brew install substrate`, desktop app via `brew install --cask substrate`.

---

## 7. What Does Not Change

- The TUI remains untouched except for the services bundle extraction
- Domain model, service layer, orchestrator, adapters, and SQLite schema are unchanged
- Bridge, config format, and session log format are unchanged
- The Go binary runs in TUI mode (default) or serve mode (flag)
- The Homebrew formula for the CLI is unchanged
