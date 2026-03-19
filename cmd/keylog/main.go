// keylog is a minimal bubbletea app that logs every tea.KeyMsg.
// Run with: go run ./cmd/keylog
// Press keys to see what bubbletea receives, then press ctrl+c to exit.
package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

type model struct {
	lines []string
}

func (m model) Init() tea.Cmd { return nil }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		line := fmt.Sprintf("%T  type=%-20s alt=%-5v string=%q", msg, msg.Type, msg.Alt, msg.String())
		m.lines = append(m.lines, line)
		if len(m.lines) > 20 {
			m.lines = m.lines[len(m.lines)-20:]
		}
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	var b strings.Builder
	b.WriteString("Press keys to inspect (ctrl+c to quit):\n\n")
	for _, l := range m.lines {
		b.WriteString(l)
		b.WriteByte('\n')
	}
	return b.String()
}

func main() {
	p := tea.NewProgram(model{}, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
