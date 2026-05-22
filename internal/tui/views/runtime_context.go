package views

import (
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/tuilog"
)

// RuntimeContext holds non-service runtime state that the TUI needs.
// These values are set once at startup and do not change during the
// lifetime of the App.
type RuntimeContext struct {
	Cfg                           *config.Config
	SettingsData                  SettingsSnapshot
	LogStore                      *tuilog.Store
	LogToasts                     <-chan tuilog.ToastEntry
	InstanceID                    string
	WorkspaceID                   string
	WorkspaceDir                  string
	WorkspaceName                 string
	StartupIntegrationsInProgress bool
}
