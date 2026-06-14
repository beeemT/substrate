package views

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/beeemT/substrate/internal/config"
	daemonclient "github.com/beeemT/substrate/internal/daemon/client"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

type ManageDaemonsOverlay struct {
	cfg    *config.Config
	styles styles.Styles
	send   func(tea.Msg)

	width, height int
	active        bool
	cursor        int
	message       string
	errorMessage  string
	adding        bool
	addBuffer     string
}

func NewManageDaemonsOverlay(cfg *config.Config, st styles.Styles, send ...func(tea.Msg)) ManageDaemonsOverlay {
	var sender func(tea.Msg)
	if len(send) > 0 {
		sender = send[0]
	}
	return ManageDaemonsOverlay{cfg: cfg, styles: st, send: sender}
}

func (m *ManageDaemonsOverlay) SetConfig(cfg *config.Config) { m.cfg = cfg }

func (m *ManageDaemonsOverlay) SetSize(w, h int) {
	m.width = w
	m.height = h
}

func (m *ManageDaemonsOverlay) Open() tea.Cmd {
	m.active = true
	m.cursor = 0
	m.message = ""
	m.errorMessage = ""
	m.adding = false
	m.addBuffer = ""
	return nil
}

func (m *ManageDaemonsOverlay) Close() {
	m.active = false
	m.message = ""
	m.errorMessage = ""
	m.adding = false
	m.addBuffer = ""
}

func (m ManageDaemonsOverlay) Active() bool { return m.active }

func (m ManageDaemonsOverlay) entries() []daemonOverlayEntry {
	if m.cfg == nil || len(m.cfg.TUI.Daemons) == 0 {
		return nil
	}
	entries := make([]daemonOverlayEntry, 0, len(m.cfg.TUI.Daemons))
	for name, entry := range m.cfg.TUI.Daemons {
		entries = append(entries, daemonOverlayEntry{Name: name, Entry: entry})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Name == "local" {
			return true
		}
		if entries[j].Name == "local" {
			return false
		}
		return entries[i].Name < entries[j].Name
	})
	return entries
}

type daemonOverlayEntry struct {
	Name  string
	Entry config.DaemonRegistryEntry
}

func (m ManageDaemonsOverlay) selected() (daemonOverlayEntry, bool) {
	entries := m.entries()
	if len(entries) == 0 || m.cursor < 0 || m.cursor >= len(entries) {
		return daemonOverlayEntry{}, false
	}
	return entries[m.cursor], true
}

func (m *ManageDaemonsOverlay) Update(msg tea.Msg) (ManageDaemonsOverlay, tea.Cmd) {
	if !m.active {
		return *m, nil
	}
	if result, ok := msg.(DaemonRegistryResultMsg); ok {
		m.message = result.Message
		m.errorMessage = ""
		if result.Err != nil {
			m.errorMessage = result.Err.Error()
		}
		if result.Config != nil {
			m.cfg = result.Config
		}
		entries := m.entries()
		if m.cursor >= len(entries) {
			m.cursor = maxInt(0, len(entries)-1)
		}
		return *m, nil
	}
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return *m, nil
	}
	if m.adding {
		return m.updateAdding(key)
	}
	switch key.String() {
	case "esc":
		return *m, func() tea.Msg { return CloseOverlayMsg{} }
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.entries())-1 {
			m.cursor++
		}
	case "enter", "s":
		entry, ok := m.selected()
		if !ok {
			return *m, nil
		}
		return *m, SwitchDaemonRegistryCmd(m.cfg, entry.Name)
	case "d", "x":
		entry, ok := m.selected()
		if !ok {
			return *m, nil
		}
		return *m, RemoveDaemonRegistryCmd(m.cfg, entry.Name)
	case "t":
		entry, ok := m.selected()
		if !ok {
			return *m, nil
		}
		return *m, TestDaemonRegistryCmd(m.cfg, entry.Name, m.send)
	case "a":
		m.adding = true
		m.addBuffer = ""
		m.message = "Enter: name address [tokenRef]"
		m.errorMessage = ""
	}
	return *m, nil
}

func (m *ManageDaemonsOverlay) updateAdding(key tea.KeyMsg) (ManageDaemonsOverlay, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.adding = false
		m.addBuffer = ""
		m.message = ""
		return *m, nil
	case "enter":
		parts := strings.Fields(m.addBuffer)
		if len(parts) < 2 {
			m.errorMessage = "expected: name address [tokenRef]"
			return *m, nil
		}
		entry := config.DaemonRegistryEntry{Kind: "remote", Address: parts[1], Label: parts[0]}
		if len(parts) > 2 {
			entry.TokenRef = parts[2]
		}
		m.adding = false
		m.addBuffer = ""
		return *m, AddDaemonRegistryCmd(m.cfg, parts[0], entry)
	case "backspace", "ctrl+h":
		if len(m.addBuffer) > 0 {
			m.addBuffer = m.addBuffer[:len(m.addBuffer)-1]
		}
	default:
		if s := key.String(); len(s) == 1 {
			m.addBuffer += s
		}
	}
	return *m, nil
}

func (m ManageDaemonsOverlay) View() string {
	entries := m.entries()
	var body []string
	body = append(body, m.styles.Title.Render("Manage Daemons"))
	body = append(body, "")
	if len(entries) == 0 {
		body = append(body, m.styles.Muted.Render("No daemon entries configured."))
	}
	active := ""
	if m.cfg != nil {
		active = m.cfg.TUI.ActiveDaemon
	}
	for i, item := range entries {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		selected := ""
		if item.Name == active || (active == "" && item.Name == "local") {
			selected = " active"
		}
		locked := ""
		if item.Name == "local" || item.Entry.AutoManaged {
			locked = " auto"
		}
		line := fmt.Sprintf("%s%s [%s]%s%s", cursor, item.Name, item.Entry.Kind, selected, locked)
		body = append(body, line)
		addr := strings.TrimSpace(item.Entry.Address)
		if addr == "" {
			addr = "local socket"
		}
		body = append(body, m.styles.Muted.Render("    "+addr))
	}
	if m.adding {
		body = append(body, "", "Add daemon: "+m.addBuffer)
	}
	if m.message != "" {
		body = append(body, "", m.styles.Success.Render(m.message))
	}
	if m.errorMessage != "" {
		body = append(body, "", m.styles.Error.Render(m.errorMessage))
	}
	hints := "[a] add  [enter/s] switch  [t] test  [d] remove  [Esc] close"
	frameWidth := maxInt(1, m.width)
	bodyWidth := maxInt(1, frameWidth-4)
	bodyHeight := maxInt(1, m.height-4)
	content := fitViewBox(lipgloss.JoinVertical(lipgloss.Left, body...), bodyWidth, bodyHeight)
	return components.RenderOverlayFrame(m.styles, frameWidth, components.OverlayFrameSpec{
		HeaderLines: []string{"Daemons"},
		Body:        content,
		Footer:      hints,
	})
}

type DaemonRegistryResultMsg struct {
	Config  *config.Config
	Message string
	Err     error
}

func AddDaemonRegistryCmd(cfg *config.Config, name string, entry config.DaemonRegistryEntry) tea.Cmd {
	return func() tea.Msg {
		cfgPath, err := config.ConfigPath()
		if err == nil {
			err = config.AddAndSaveDaemonRegistryEntry(cfgPath, cfg, name, entry)
		}
		return daemonRegistryResult(cfg, "Daemon added: "+name, err)
	}
}

func SwitchDaemonRegistryCmd(cfg *config.Config, name string) tea.Cmd {
	return func() tea.Msg {
		cfgPath, err := config.ConfigPath()
		if err == nil {
			err = config.SwitchAndSaveActiveDaemon(cfgPath, cfg, name)
		}
		return daemonRegistryResult(cfg, "Active daemon switched to "+name+". Restart the TUI to reconnect.", err)
	}
}

func RemoveDaemonRegistryCmd(cfg *config.Config, name string) tea.Cmd {
	return func() tea.Msg {
		cfgPath, err := config.ConfigPath()
		if err == nil {
			err = config.RemoveAndSaveDaemonRegistryEntry(cfgPath, cfg, name)
		}
		return daemonRegistryResult(cfg, "Daemon removed: "+name, err)
	}
}

// daemonTestTimeout bounds dial+health for the "test daemon" action. The
// connection is lazy, so the budget mostly covers the Health RPC; keep it
// short so a misconfigured entry surfaces immediately.
const daemonTestTimeout = 5 * time.Second

// TestDaemonRegistryCmd checks whether the daemon's Health RPC responds. When
// a sender is available, the RPC runs off the Bubble Tea command path so a slow
// or unreachable daemon cannot block the TUI update loop. Tests that do not own
// a Program can pass nil and receive the result synchronously.
func TestDaemonRegistryCmd(cfg *config.Config, name string, send func(tea.Msg)) tea.Cmd {
	return func() tea.Msg {
		if send != nil {
			go func() { send(runDaemonHealthCheck(cfg, name)) }()
			return nil
		}
		return runDaemonHealthCheck(cfg, name)
	}
}

func runDaemonHealthCheck(cfg *config.Config, name string) tea.Msg {
	if cfg == nil {
		return daemonRegistryResult(nil, "", fmt.Errorf("config is required"))
	}
	entry, ok := cfg.TUI.Daemons[name]
	if !ok {
		return daemonRegistryResult(cfg, "", fmt.Errorf("daemon %q is not configured", name))
	}
	token, err := config.DaemonAccessToken(cfg, config.OSKeychainStore{}, name)
	if err != nil {
		return daemonRegistryResult(cfg, "", err)
	}
	address := strings.TrimSpace(entry.Address)
	if address == "" {
		// Auto-managed local daemons do not store an address in TUI
		// config; resolve the default unix socket so the dial does not
		// fail on an empty target.
		socketPath, socketErr := defaultLocalSocketPath(cfg)
		if socketErr != nil {
			return daemonRegistryResult(cfg, "", socketErr)
		}
		address = "unix://" + socketPath
	}
	// Bound the dial+health probes so a dead address cannot wedge the TUI
	// message loop indefinitely; surface the error back to the overlay.
	dialCtx, cancel := context.WithTimeout(context.Background(), daemonTestTimeout)
	defer cancel()
	client, err := daemonclient.Dial(dialCtx, address, token)
	if err != nil {
		return daemonRegistryResult(cfg, "", err)
	}
	defer client.Close()
	if _, err := client.Health(dialCtx); err != nil {
		return daemonRegistryResult(cfg, "", err)
	}
	return daemonRegistryResult(cfg, "Daemon reachable: "+name, nil)
}

// defaultLocalSocketPath returns the default daemon unix socket path
// for the local auto-managed entry. Mirrors cmd/substrate's
// daemonSocketPath: honor an explicit bind override, otherwise fall
// back to the global runtime dir.
func defaultLocalSocketPath(cfg *config.Config) (string, error) {
	if cfg != nil && strings.TrimSpace(cfg.Daemon.Runtime.Bind.SocketPath) != "" {
		return cfg.Daemon.Runtime.Bind.SocketPath, nil
	}
	globalDir, err := config.GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(globalDir, "run", "local.sock"), nil
}

func daemonRegistryResult(cfg *config.Config, message string, err error) DaemonRegistryResultMsg {
	if err != nil {
		return DaemonRegistryResultMsg{Config: cfg, Err: err}
	}
	return DaemonRegistryResultMsg{Config: cfg, Message: message}
}
