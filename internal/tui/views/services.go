package views

import "github.com/beeemT/substrate/internal/logic"

// Services is the views alias for the daemon-owned service graph. The actual
// struct definition lives in internal/logic so the daemon can build and
// rebuild it without Bubble Tea imports. The TUI consumes it read-only via
// the ServiceProvider interface.
type Services = logic.Services
