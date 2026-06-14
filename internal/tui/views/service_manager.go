package views

import (
	atomic "github.com/beeemT/go-atomic"
	"github.com/beeemT/substrate/internal/config"
	"github.com/beeemT/substrate/internal/logic"
	"github.com/beeemT/substrate/internal/repository"
)

// ServiceManager is the TUI's view of the daemon-owned service graph. It
// embeds *logic.ServiceManager so all build/rebuild/init/lifecycle methods
// are promoted from the logic layer. The only view-specific behaviour added
// here is Settings(), which returns the view-level SettingsService interface
// so the TUI can call view-specific methods on it (Snapshot, Save, etc.).
type ServiceManager struct {
	*logic.ServiceManager
}

// NewServiceManager creates a new TUI ServiceManager wrapping the
// daemon-owned logic.ServiceManager. Construction does not import Bubble Tea;
// the logic layer can be initialised headlessly by the daemon. The settings
// factory is wired so the daemon-side graph can construct the TUI's
// settings service when none is supplied via the current Services.
func NewServiceManager(
	transacter atomic.Transacter[repository.Resources],
	eventRepo repository.EventRepository,
) *ServiceManager {
	sm := &ServiceManager{
		ServiceManager: logic.NewServiceManager(transacter, eventRepo),
	}
	sm.ServiceManager.WithSettingsFactory(func(t atomic.Transacter[repository.Resources]) logic.SettingsService {
		return NewSettingsService(t, config.OSKeychainStore{}, sm)
	})
	return sm
}

// Settings returns the view-level SettingsService interface. If the
// underlying logic.SettingsService is also a view.SettingsService, it is
// returned; otherwise nil. This keeps the type narrow to the TUI's contract
// while still sourcing the implementation from the daemon-owned graph.
func (sm *ServiceManager) Settings() SettingsService {
	if sm == nil || sm.ServiceManager == nil {
		return nil
	}
	svcs := sm.GetServices()
	if svcs == nil {
		return nil
	}
	if ss, ok := svcs.Settings.(SettingsService); ok {
		return ss
	}
	return nil
}

func (sm *ServiceManager) EventClient() EventStreamClient { return nil }

func (sm *ServiceManager) LogClient() SessionLogClient { return nil }

func (sm *ServiceManager) AutonomousClient() AutonomousClient { return nil }

func (sm *ServiceManager) WorkspaceClient() WorkspaceClient { return nil }

// Verify ServiceManager implements ServiceProvider at compile time.
var _ ServiceProvider = (*ServiceManager)(nil)
