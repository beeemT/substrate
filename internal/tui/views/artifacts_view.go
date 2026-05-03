package views

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// ArtifactsModel is an accordion list view for PR/MR artifacts.
type ArtifactsModel struct { //nolint:recvcheck // Bubble Tea: Update returns value, View on value receiver
	styles        styles.Styles
	items         []ArtifactItem
	cursor        int
	expandedSet   map[string]bool
	viewport      viewport.Model
	width         int
	height        int
	workItemID    string
	workItemState domain.SessionState
}

// NewArtifactsModel creates a new artifacts accordion model.
func NewArtifactsModel(st styles.Styles) ArtifactsModel {
	return ArtifactsModel{
		styles:      st,
		cursor:      -1,
		expandedSet: make(map[string]bool),
	}
}

// SetSize updates the available dimensions and syncs the viewport.
func (m *ArtifactsModel) SetSize(w, h int) {
	m.width = w
	m.height = h
	m.syncViewport()
}

// SetItems replaces the current item list while preserving expansion state for
// stable artifact IDs across periodic refreshes.
// When there are 2 or 3 items, all are auto-expanded.
func (m *ArtifactsModel) SetItems(items []ArtifactItem) {
	previousExpanded := m.expandedSet
	m.items = items
	m.expandedSet = make(map[string]bool, len(previousExpanded))
	for _, item := range items {
		key := artifactExpansionKey(item)
		if previousExpanded[key] || len(items) == 2 || len(items) == 3 {
			m.expandedSet[key] = true
		}
	}
	if len(items) > 0 {
		if m.cursor < 0 {
			m.cursor = 0
		} else if m.cursor >= len(items) {
			m.cursor = len(items) - 1
		}
	} else {
		m.cursor = -1
	}
	m.syncViewport()
	// Auto-expanded cards may push the focused row below the viewport — scroll to make it visible.
	m.ensureCursorVisible()
}

func artifactExpansionKey(item ArtifactItem) string {
	if item.ID != "" {
		return item.ID
	}
	return strings.Join([]string{item.Provider, item.RepoName, item.Ref}, "\x00")
}

// SetWorkItem binds the artifacts view to a specific work item and its current
// lifecycle state. The state drives which follow-up keybinds are enabled.
func (m *ArtifactsModel) SetWorkItem(workItemID string, state domain.SessionState) {
	m.workItemID = workItemID
	m.workItemState = state
}

// reviewFollowupEnabled reports whether the review follow-up flow may be invoked
// from the artifacts view.
func (m ArtifactsModel) reviewFollowupEnabled() bool {
	if len(m.items) == 0 {
		return false
	}
	switch m.workItemState {
	case domain.SessionCompleted, domain.SessionReviewing:
		return true
	}
	return false
}

// syncViewport rebuilds the viewport content from the current state.
// Must be called from pointer-receiver methods (SetSize, SetItems) and
// at the end of Update after any state change. The viewport survives
// across frames only when set via pointer receiver or Update return value.
func (m *ArtifactsModel) syncViewport() {
	m.viewport.Width = m.width
	headerLines := m.headerLineCount()
	vpHeight := m.height - headerLines
	if vpHeight < 1 {
		vpHeight = 1
	}
	m.viewport.Height = vpHeight
	m.viewport.SetContent(m.buildBody())
}

func (m ArtifactsModel) headerLineCount() int {
	if m.width <= 0 {
		return 0
	}
	header := m.renderHeader()
	return strings.Count(header, "\n") + 1
}

func (m ArtifactsModel) renderHeader() string {
	return components.RenderHeaderBlock(m.styles, components.HeaderBlockSpec{
		Title:   "Artifacts",
		Meta:    "Pull requests and merge requests",
		Width:   m.width,
		Divider: true,
	})
}

func (m ArtifactsModel) buildBody() string {
	switch {
	case len(m.items) == 0:
		return m.styles.Muted.Render("No artifacts")
	case len(m.items) == 1:
		return m.renderExpandedCard(0)
	default:
		return m.renderAccordion()
	}
}

// Cursor returns the current cursor position (for testing).
func (m ArtifactsModel) Cursor() int { return m.cursor }

// Update handles key and mouse input.
func (m ArtifactsModel) Update(msg tea.Msg) (ArtifactsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		if len(m.items) == 0 {
			return m, nil
		}
		changed := false
		switch msg.String() {
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
				changed = true
			}
		case "down", "j":
			if m.cursor < len(m.items)-1 {
				m.cursor++
				changed = true
			}
		case "right", "l":
			key := artifactExpansionKey(m.items[m.cursor])
			if !m.expandedSet[key] {
				m.expandedSet[key] = true
				changed = true
			}
		case " ":
			key := artifactExpansionKey(m.items[m.cursor])
			m.expandedSet[key] = !m.expandedSet[key]
			changed = true
		case "o":
			if url := m.items[m.cursor].URL; url != "" {
				return m, func() tea.Msg { return OpenExternalURLMsg{URL: url} }
			}
		case "O":
			if len(m.items) == 1 {
				if url := m.items[0].URL; url != "" {
					return m, func() tea.Msg { return OpenExternalURLMsg{URL: url} }
				}
			} else {
				items := m.items
				return m, func() tea.Msg { return OpenArtifactLinksMsg{Items: items} }
			}
		case "f":
			if m.reviewFollowupEnabled() {
				items := m.items
				workItemID := m.workItemID
				return m, func() tea.Msg {
					return FetchReviewCommentsMsg{WorkItemID: workItemID, Items: items}
				}
			}
		}
		if changed {
			m.syncViewport()
			m.ensureCursorVisible()
		}

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
				var cmd tea.Cmd
				m.viewport, cmd = m.viewport.Update(msg)
				return m, cmd
			}
		}
	}

	return m, nil
}

// ensureCursorVisible scrolls the viewport so the focused item row is visible.
// For expanded items, the row header is placed at the top of the viewport.
// For collapsed items, the row is placed within the viewport (may be below top).
func (m *ArtifactsModel) ensureCursorVisible() {
	if m.cursor < 0 || len(m.items) == 0 {
		return
	}
	// Count lines before the cursor row to find its position in the document.
	linesBefore := 0
	for i := 0; i < m.cursor; i++ {
		linesBefore++ // collapsed row
		if m.expandedSet[artifactExpansionKey(m.items[i])] {
			linesBefore += strings.Count(m.renderExpandedCard(i), "\n") + 1
		}
	}
	// The cursor row is at linesBefore.
	if m.expandedSet[artifactExpansionKey(m.items[m.cursor])] {
		// For expanded items, scroll so the row header is at the top.
		if linesBefore < m.viewport.YOffset {
			m.viewport.SetYOffset(linesBefore)
		} else if linesBefore >= m.viewport.YOffset+m.viewport.Height {
			m.viewport.SetYOffset(linesBefore - m.viewport.Height + 1)
		}
	} else {
		// For collapsed items, ensure the row is within the viewport.
		if linesBefore < m.viewport.YOffset {
			m.viewport.SetYOffset(linesBefore)
		} else if linesBefore >= m.viewport.YOffset+m.viewport.Height {
			m.viewport.SetYOffset(linesBefore - m.viewport.Height + 1)
		}
	}
}

// View renders the accordion list inside a scrollable viewport.
func (m ArtifactsModel) View() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	header := m.renderHeader()
	return fitViewBox(header+"\n"+m.viewport.View(), m.width, m.height)
}

// KeybindHints returns the status bar hints for the artifacts view.
func (m ArtifactsModel) KeybindHints() []KeybindHint {
	if len(m.items) == 0 {
		return nil
	}
	hints := []KeybindHint{{Key: "↑↓", Label: "Navigate"}}
	if len(m.items) > 1 {
		hints = append(hints, KeybindHint{Key: "Space", Label: "Expand/collapse"})
	}
	if m.cursor >= 0 && m.cursor < len(m.items) && m.items[m.cursor].URL != "" {
		hints = append(hints, KeybindHint{Key: "o", Label: "Open in browser"})
	}
	hints = append(hints, KeybindHint{Key: "O", Label: "PR links"})
	if m.reviewFollowupEnabled() {
		hints = append(hints, KeybindHint{Key: "f", Label: "Follow up on review"})
	}
	return hints
}

func (m ArtifactsModel) renderAccordion() string {
	var rows []string
	for i, item := range m.items {
		rows = append(rows, m.renderCollapsedRow(i, item))
		if m.expandedSet[artifactExpansionKey(item)] {
			rows = append(rows, m.renderExpandedCard(i))
		}
	}
	return strings.Join(rows, "\n")
}

func (m ArtifactsModel) renderCollapsedRow(idx int, item ArtifactItem) string {
	stateTag := item.State
	if item.Draft && stateTag != "merged" && stateTag != "closed" {
		stateTag = "draft"
	}
	reviewSummary := m.reviewSummaryText(item)
	indicator := ">"
	if m.expandedSet[artifactExpansionKey(item)] {
		indicator = "⌄"
	}
	line := fmt.Sprintf("%s %s  %s  %s  [%s]", indicator, item.Ref, item.RepoName, item.Branch, stateTag)
	if reviewSummary != "" {
		line += "  " + reviewSummary
	}
	ciSummary := m.ciSummaryText(item)
	if ciSummary != "" {
		line += "  " + ciSummary
	}
	line = ansi.Truncate(line, m.width, "…")

	if idx == m.cursor {
		return m.styles.SidebarSelected.Width(m.width).Render(line)
	}
	return m.styles.SettingsText.Render(line)
}

// reviewSummaryText returns a compact review status string for the collapsed row.
func (m ArtifactsModel) reviewSummaryText(item ArtifactItem) string {
	if len(item.Reviews) == 0 {
		return ""
	}
	hasApproved := false
	hasChangesRequested := false
	for _, r := range item.Reviews {
		switch r.State {
		case "approved":
			hasApproved = true
		case "changes_requested":
			hasChangesRequested = true
		}
	}
	switch {
	case hasChangesRequested:
		return m.styles.Error.Render("✗ review")
	case hasApproved:
		return m.styles.Success.Render("✓ review")
	default:
		return m.styles.Muted.Render("◐ review")
	}
}

// ciSummaryText returns a compact CI status string for the collapsed row.
func (m ArtifactsModel) ciSummaryText(item ArtifactItem) string {
	if len(item.Checks) == 0 {
		return ""
	}
	hasFailure := false
	hasInProgress := false
	for _, c := range item.Checks {
		if c.Conclusion == "failure" {
			hasFailure = true
		}
		if c.Status == "in_progress" || c.Status == "queued" {
			hasInProgress = true
		}
	}
	switch {
	case hasFailure:
		return m.styles.Error.Render("✗ CI")
	case hasInProgress:
		return m.styles.Muted.Render("○ CI")
	default:
		return m.styles.Success.Render("✓ CI")
	}
}

func (m ArtifactsModel) renderExpandedCard(idx int) string {
	item := m.items[idx]
	innerWidth := components.CalloutInnerWidth(m.styles, m.width)

	rows := []string{
		renderKeyValueLine(m.styles, innerWidth, "Kind", item.Kind),
		renderKeyValueLine(m.styles, innerWidth, "Repo", item.RepoName),
		renderKeyValueLine(m.styles, innerWidth, "Ref", item.Ref),
		renderKeyValueLine(m.styles, innerWidth, "Branch", item.Branch),
		renderKeyValueLine(m.styles, innerWidth, "State", item.State),
	}
	if item.URL != "" {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "URL", item.URL))
	}
	if !item.CreatedAt.IsZero() {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "Created", formatAbsoluteTime(item.CreatedAt)))
	}
	if !item.UpdatedAt.IsZero() {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "Updated", formatAbsoluteTime(item.UpdatedAt)))
	}
	if item.MergedAt != nil {
		rows = append(rows, renderKeyValueLine(m.styles, innerWidth, "Merged", formatAbsoluteTime(*item.MergedAt)))
	}
	if len(item.Reviews) > 0 {
		rows = append(rows, "") // blank separator line
		rows = append(rows, m.styles.SectionLabel.Render("Review"))
		for _, r := range item.Reviews {
			if r.ReviewerLogin == "__unresolved_threads__" {
				rows = append(rows, renderReviewLine(m.styles, innerWidth, "unresolved threads", r.State, r.SubmittedAt))
			} else {
				rows = append(rows, renderReviewLine(m.styles, innerWidth, r.ReviewerLogin, r.State, r.SubmittedAt))
			}
		}
	}
	if len(item.Checks) > 0 {
		rows = append(rows, "") // blank separator line
		rows = append(rows, m.styles.SectionLabel.Render("CI"))
		for _, c := range item.Checks {
			rows = append(rows, renderCheckLine(m.styles, innerWidth, c))
		}
	}

	return components.RenderCallout(m.styles, components.CalloutSpec{
		Body:    strings.Join(rows, "\n"),
		Width:   m.width,
		Variant: components.CalloutCard,
	})
}

func renderReviewLine(st styles.Styles, width int, reviewer, state string, submittedAt time.Time) string {
	var icon string
	switch state {
	case "approved":
		icon = st.Success.Render("✓")
	case "changes_requested":
		icon = st.Error.Render("✗")
	default:
		icon = st.Muted.Render("◌")
	}
	ago := formatRelativeTime(submittedAt)
	line := fmt.Sprintf("  %s %-16s %-20s %s", icon, reviewer, state, ago)
	return ansi.Truncate(line, width, "…")
}

func formatRelativeTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}

func renderCheckLine(st styles.Styles, width int, check ArtifactCheck) string {
	var icon string
	switch {
	case check.Conclusion == "failure":
		icon = st.Error.Render("✗")
	case check.Conclusion == "success":
		icon = st.Success.Render("✓")
	case check.Status == "in_progress":
		icon = st.Muted.Render("○")
	case check.Conclusion == "skipped":
		icon = st.Muted.Render("–")
	default:
		icon = st.Muted.Render("◌")
	}
	line := fmt.Sprintf("  %s %-30s %s", icon, check.Name, check.Conclusion)
	if check.Conclusion == "" && check.Status != "" {
		line = fmt.Sprintf("  %s %-30s %s", icon, check.Name, check.Status)
	}
	return ansi.Truncate(line, width, "…")
}
