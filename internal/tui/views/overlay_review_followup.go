package views

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/beeemT/substrate/internal/adapter"
	"github.com/beeemT/substrate/internal/tui/components"
	"github.com/beeemT/substrate/internal/tui/styles"
)

// reviewFollowupStage identifies which sub-view of the overlay is active.
type reviewFollowupStage int

const (
	reviewFollowupStageLoading  reviewFollowupStage = iota // fetching review comments
	reviewFollowupStagePicker                              // PR picker (only when >1 PR with unresolved comments)
	reviewFollowupStageSelector                            // comment selection split view
	reviewFollowupStageConfirm                             // re-plan confirmation modal
)

// ReviewCommentsStaleMaxAge is how old a fetch may be before dispatch silently re-fetches.
const ReviewCommentsStaleMaxAge = 5 * time.Minute

const reviewFollowupMaxFrameWidth = 120

// reviewSelectorRowKind classifies a row in the comment-selection list.
type reviewSelectorRowKind int

const (
	rowKindRepoHeader reviewSelectorRowKind = iota
	rowKindFileHeader
	rowKindComment
)

// reviewSelectorRow is a flat row in the left selection pane.
type reviewSelectorRow struct {
	kind      reviewSelectorRowKind
	itemID    string // PR/MR ArtifactItem.ID; set on every row for grouping math
	repoName  string
	filePath  string // empty for repo headers and General-section comments
	commentID string // set on rowKindComment only
}

// ReviewFollowupModel owns the multi-stage overlay used to dispatch review-driven follow-ups.
//
// Lifecycle:
//
//	OpenLoading -> ReviewCommentsFetched -> picker (or selector if 1 PR / no PRs aborted)
//	  picker -> selector -> dispatchAddress | dispatchReplan
//	  dispatchReplan -> confirm -> emit FollowUpFromReviewReplanMsg
//	  dispatchAddress -> emit FollowUpFromReviewAddressMsg
//
//nolint:recvcheck // Bubble Tea: View on value receiver, mutating helpers on pointer
type ReviewFollowupModel struct {
	active     bool
	stage      reviewFollowupStage
	workItemID string

	// items is the ORIGINAL set of artifacts the fetch was launched for; preserved
	// so re-fetch can replay against the same scope.
	items []ArtifactItem

	// scopedItems is filtered to PRs included in dispatch (after picker).
	scopedItems []ArtifactItem

	// commentsByItem keys by ArtifactItem.ID; only includes unresolved comments.
	commentsByItem map[string][]adapter.ReviewComment
	fetchedAt      time.Time

	// pickerSelected: ArtifactItem.ID -> included in selection. Defaults true.
	pickerSelected map[string]bool
	pickerCursor   int
	pickerItems    []ArtifactItem // PRs with at least 1 unresolved comment

	// selectorRows is the flat row layout of the selection pane.
	selectorRows []reviewSelectorRow
	// commentSelected: comment.ID -> selected. Defaults true at fetch time.
	commentSelected map[string]bool
	selectorCursor  int

	spinner spinner.Model

	width, height int
	styles        styles.Styles
}

// NewReviewFollowupModel constructs the overlay in inactive state.
func NewReviewFollowupModel(st styles.Styles) ReviewFollowupModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return ReviewFollowupModel{
		styles:  st,
		spinner: sp,
	}
}

// Active reports whether the overlay should be rendered and receive input.
func (m ReviewFollowupModel) Active() bool { return m.active }

// Stage exposes the current sub-view (used by tests / status hints).
func (m ReviewFollowupModel) Stage() reviewFollowupStage { return m.stage }

// FetchedAt exposes the fetch timestamp; zero when never fetched.
func (m ReviewFollowupModel) FetchedAt() time.Time { return m.fetchedAt }

// IsStale reports whether the cached comments are older than the staleness window.
// A zero fetchedAt is never reported as stale (it is reported as missing data instead).
func (m ReviewFollowupModel) IsStale(now time.Time) bool {
	if m.fetchedAt.IsZero() {
		return false
	}
	return now.Sub(m.fetchedAt) > ReviewCommentsStaleMaxAge
}

// SetSize stores the available terminal dimensions.
func (m *ReviewFollowupModel) SetSize(w, h int) {
	m.width = w
	m.height = h
}

// Close hides the overlay and clears transient state.
func (m *ReviewFollowupModel) Close() {
	m.active = false
	m.stage = reviewFollowupStageLoading
	m.workItemID = ""
	m.items = nil
	m.scopedItems = nil
	m.commentsByItem = nil
	m.fetchedAt = time.Time{}
	m.pickerSelected = nil
	m.pickerCursor = 0
	m.pickerItems = nil
	m.selectorRows = nil
	m.commentSelected = nil
	m.selectorCursor = 0
}

// OpenLoading puts the overlay into the loading stage for the given work item and
// returns the spinner-tick command to start animation.
func (m *ReviewFollowupModel) OpenLoading(workItemID string, items []ArtifactItem) tea.Cmd {
	m.Close()
	m.active = true
	m.stage = reviewFollowupStageLoading
	m.workItemID = workItemID
	m.items = items
	return m.spinner.Tick
}

// ApplyFetchResult populates the overlay with fetched comments, deciding the next stage.
//
// Returned bool indicates whether the overlay should remain visible:
//   - false → caller closes overlay and surfaces a toast (no PRs with unresolved comments).
//   - true  → overlay transitions to picker (>1 PR) or selector (exactly 1 PR).
//
// Selection defaults: every PR is checked; every comment is checked.
func (m *ReviewFollowupModel) ApplyFetchResult(commentsByItem map[string][]adapter.ReviewComment, fetchedAt time.Time) bool {
	m.commentsByItem = commentsByItem
	m.fetchedAt = fetchedAt

	// Filter items list to those with at least one unresolved comment.
	withUnresolved := make([]ArtifactItem, 0, len(m.items))
	for _, it := range m.items {
		if len(commentsByItem[it.ID]) > 0 {
			withUnresolved = append(withUnresolved, it)
		}
	}
	if len(withUnresolved) == 0 {
		return false
	}

	m.pickerItems = withUnresolved
	m.pickerSelected = make(map[string]bool, len(withUnresolved))
	for _, it := range withUnresolved {
		m.pickerSelected[it.ID] = true
	}

	// Pre-select every comment.
	m.commentSelected = make(map[string]bool)
	for _, comments := range commentsByItem {
		for _, c := range comments {
			m.commentSelected[c.ID] = true
		}
	}

	if len(withUnresolved) == 1 {
		m.scopedItems = withUnresolved
		m.stage = reviewFollowupStageSelector
		m.rebuildSelectorRows()
		m.selectorCursor = m.firstCommentRow()
	} else {
		m.stage = reviewFollowupStagePicker
		m.pickerCursor = 0
	}
	return true
}

// MergeRefetch reapplies the user's selections (by ID) onto a freshly-fetched dataset.
// New comments default to deselected. Disappeared comments are silently dropped.
// Returns the count of previously-selected comments that no longer exist.
func (m *ReviewFollowupModel) MergeRefetch(commentsByItem map[string][]adapter.ReviewComment, fetchedAt time.Time) int {
	prevSelected := m.commentSelected
	m.commentsByItem = commentsByItem
	m.fetchedAt = fetchedAt

	dropped := 0
	stillExists := make(map[string]bool)
	for _, comments := range commentsByItem {
		for _, c := range comments {
			stillExists[c.ID] = true
		}
	}
	for id, sel := range prevSelected {
		if sel && !stillExists[id] {
			dropped++
		}
	}

	m.commentSelected = make(map[string]bool)
	for _, comments := range commentsByItem {
		for _, c := range comments {
			// Preserve previous selection state; default false for newcomers.
			m.commentSelected[c.ID] = prevSelected[c.ID]
		}
	}
	// Recompute pickerItems / scopedItems against the new dataset.
	withUnresolved := make([]ArtifactItem, 0, len(m.items))
	for _, it := range m.items {
		if len(commentsByItem[it.ID]) > 0 {
			withUnresolved = append(withUnresolved, it)
		}
	}
	m.pickerItems = withUnresolved
	// Drop scoped items that no longer have unresolved comments.
	scoped := make([]ArtifactItem, 0, len(m.scopedItems))
	for _, it := range m.scopedItems {
		if len(commentsByItem[it.ID]) > 0 {
			scoped = append(scoped, it)
		}
	}
	m.scopedItems = scoped
	m.rebuildSelectorRows()
	if m.selectorCursor >= len(m.selectorRows) {
		m.selectorCursor = m.firstCommentRow()
	}
	return dropped
}

// SelectedComments returns the user's current selection grouped by ArtifactItem.ID.
// Empty slices are omitted.
func (m ReviewFollowupModel) SelectedComments() map[string][]adapter.ReviewComment {
	out := make(map[string][]adapter.ReviewComment)
	for _, it := range m.scopedItems {
		var sel []adapter.ReviewComment
		for _, c := range m.commentsByItem[it.ID] {
			if m.commentSelected[c.ID] {
				sel = append(sel, c)
			}
		}
		if len(sel) > 0 {
			out[it.ID] = sel
		}
	}
	return out
}

// HasAnySelection reports whether at least one comment is currently selected.
func (m ReviewFollowupModel) HasAnySelection() bool {
	for _, it := range m.scopedItems {
		for _, c := range m.commentsByItem[it.ID] {
			if m.commentSelected[c.ID] {
				return true
			}
		}
	}
	return false
}

// WorkItemID exposes the work item the overlay is bound to.
func (m ReviewFollowupModel) WorkItemID() string { return m.workItemID }

// Items exposes the original artifact list (for re-fetch).
func (m ReviewFollowupModel) Items() []ArtifactItem { return m.items }

// ScopedItems exposes the artifacts in dispatch scope (post-picker).
func (m ReviewFollowupModel) ScopedItems() []ArtifactItem { return m.scopedItems }

// FormatPerRepo builds per-repo feedback strings for address-mode dispatch.
// Key is repoName (matches Task.RepositoryName); value is the formatted feedback.
// Repos with no selected comments are omitted.
func (m ReviewFollowupModel) FormatPerRepo() map[string]string {
	selected := m.SelectedComments()
	out := make(map[string]string, len(selected))
	for _, it := range m.scopedItems {
		comments := selected[it.ID]
		if len(comments) == 0 {
			continue
		}
		// Use the same template as the all-repos formatter, but emit only this repo.
		out[it.RepoName] = formatReviewComments(map[string][]adapter.ReviewComment{
			it.RepoName: comments,
		}, []string{it.RepoName})
	}
	return out
}

// FormatAllSelected builds a single feedback string covering every selected comment
// across every scoped repo. Used by re-plan dispatch.
func (m ReviewFollowupModel) FormatAllSelected() string {
	selected := m.SelectedComments()
	repoOrder := make([]string, 0, len(m.scopedItems))
	byRepo := make(map[string][]adapter.ReviewComment, len(selected))
	for _, it := range m.scopedItems {
		comments := selected[it.ID]
		if len(comments) == 0 {
			continue
		}
		repoOrder = append(repoOrder, it.RepoName)
		byRepo[it.RepoName] = append(byRepo[it.RepoName], comments...)
	}
	if len(byRepo) == 0 {
		return ""
	}
	return formatReviewComments(byRepo, repoOrder)
}

// formatReviewComments renders the canonical per-repo / per-file markdown block.
// repoOrder controls deterministic ordering; files within a repo are sorted alphabetically
// with the General (top-level) section first.
func formatReviewComments(byRepo map[string][]adapter.ReviewComment, repoOrder []string) string {
	var b strings.Builder
	b.WriteString("## Review comments to address\n")
	for _, repo := range repoOrder {
		comments := byRepo[repo]
		if len(comments) == 0 {
			continue
		}
		b.WriteString("\n### ")
		b.WriteString(repo)
		b.WriteString("\n")

		var general []adapter.ReviewComment
		byFile := make(map[string][]adapter.ReviewComment)
		for _, c := range comments {
			if strings.TrimSpace(c.Path) == "" {
				general = append(general, c)
				continue
			}
			byFile[c.Path] = append(byFile[c.Path], c)
		}
		if len(general) > 0 {
			b.WriteString("\n#### General\n\n")
			for _, c := range general {
				b.WriteString("- ")
				if c.ReviewerLogin != "" {
					b.WriteString(c.ReviewerLogin)
					b.WriteString(": ")
				}
				b.WriteString(strings.TrimSpace(c.Body))
				b.WriteString("\n")
			}
		}
		filePaths := make([]string, 0, len(byFile))
		for p := range byFile {
			filePaths = append(filePaths, p)
		}
		sort.Strings(filePaths)
		for _, p := range filePaths {
			b.WriteString("\n#### ")
			b.WriteString(p)
			b.WriteString("\n\n")
			for _, c := range byFile[p] {
				b.WriteString("- Line ")
				b.WriteString(fmt.Sprintf("%d", c.Line))
				b.WriteString(": ")
				b.WriteString(strings.TrimSpace(c.Body))
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

// --- View construction ---

func (m ReviewFollowupModel) frameWidth() int {
	if m.width <= 0 {
		return reviewFollowupMaxFrameWidth
	}
	return min(max(60, m.width-4), reviewFollowupMaxFrameWidth)
}

func (m ReviewFollowupModel) innerWidth() int {
	return styles.DefaultChromeMetrics.OverlayFrame.InnerWidth(m.frameWidth())
}

// frameHeight returns the body height the overlay should fill.
// chromeRows accounts for header + divider + blank + footer = 4.
func (m ReviewFollowupModel) frameHeight() int {
	if m.height <= 0 {
		return 16
	}
	const chromeRows = 6
	return max(8, m.height-chromeRows)
}

// View renders the active stage. Returns "" when inactive.
func (m ReviewFollowupModel) View() string {
	if !m.active {
		return ""
	}
	switch m.stage {
	case reviewFollowupStageLoading:
		return m.viewLoading()
	case reviewFollowupStagePicker:
		return m.viewPicker()
	case reviewFollowupStageSelector:
		return m.viewSelector()
	case reviewFollowupStageConfirm:
		return m.viewConfirm()
	}
	return ""
}

func (m ReviewFollowupModel) viewLoading() string {
	fw := m.frameWidth()
	iw := m.innerWidth()
	header := []string{
		m.styles.Title.Render("Review follow-up"),
		components.RenderOverlayDivider(m.styles, iw),
	}
	body := m.spinner.View() + " Fetching review comments…"
	body = ansi.Truncate(body, iw, "")
	footer := m.styles.Hint.Render(ansi.Truncate("[Esc] Cancel", iw, ""))
	return components.RenderOverlayFrame(m.styles, fw, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        fitViewHeight(body, m.frameHeight()),
		Footer:      footer,
		Focused:     true,
	})
}

func (m ReviewFollowupModel) viewPicker() string {
	fw := m.frameWidth()
	iw := m.innerWidth()
	header := []string{
		m.styles.Title.Render("Select PRs to address"),
		components.RenderOverlayDivider(m.styles, iw),
	}
	rows := make([]string, 0, len(m.pickerItems))
	for i, it := range m.pickerItems {
		mark := "[ ]"
		if m.pickerSelected[it.ID] {
			mark = "[x]"
		}
		count := len(m.commentsByItem[it.ID])
		line := fmt.Sprintf("%s  %s %s  (%d comment%s)", mark, it.RepoName, it.Ref, count, pluralS(count))
		if i == m.pickerCursor {
			line = m.styles.Active.Render("▶ " + line)
		} else {
			line = "  " + m.styles.SettingsText.Render(line)
		}
		rows = append(rows, ansi.Truncate(line, iw, ""))
	}
	body := strings.Join(rows, "\n")
	footer := m.styles.Hint.Render(ansi.Truncate(
		"[↑↓] Move  [Space] Toggle  [a] All  [n] None  [Enter] Continue  [Esc] Cancel",
		iw, ""))
	return components.RenderOverlayFrame(m.styles, fw, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        fitViewHeight(body, m.frameHeight()),
		Footer:      footer,
		Focused:     true,
	})
}

func (m ReviewFollowupModel) viewSelector() string {
	fw := m.frameWidth()
	iw := m.innerWidth()
	header := []string{
		m.styles.Title.Render("Review comments"),
		components.RenderOverlayDivider(m.styles, iw),
	}
	bodyHeight := m.frameHeight()
	// Split width 50/50 with 1 col separator.
	leftWidth := max(20, (iw-1)/2)
	rightWidth := max(20, iw-leftWidth-1)

	leftBody := m.renderSelectorList(leftWidth, bodyHeight)
	rightBody := m.renderPreview(rightWidth, bodyHeight)

	rows := make([]string, 0, bodyHeight)
	leftLines := strings.Split(leftBody, "\n")
	rightLines := strings.Split(rightBody, "\n")
	for i := 0; i < bodyHeight; i++ {
		var l, r string
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		l = padRight(ansi.Truncate(l, leftWidth, "…"), leftWidth)
		r = ansi.Truncate(r, rightWidth, "…")
		rows = append(rows, l+" "+r)
	}
	body := strings.Join(rows, "\n")

	footer := m.styles.Hint.Render(ansi.Truncate(
		"[↑↓] Move  [Space] Toggle  [a] All  [n] None  [Enter] Address  [p] Re-plan  [Esc] Cancel",
		iw, ""))
	return components.RenderOverlayFrame(m.styles, fw, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        body,
		Footer:      footer,
		Focused:     true,
	})
}

func (m ReviewFollowupModel) renderSelectorList(width, height int) string {
	if len(m.selectorRows) == 0 {
		return m.styles.Muted.Render("No comments")
	}
	// Window the rows around the cursor.
	start := 0
	if m.selectorCursor >= height {
		start = m.selectorCursor - height + 1
	}
	end := start + height
	if end > len(m.selectorRows) {
		end = len(m.selectorRows)
	}
	out := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		row := m.selectorRows[i]
		var line string
		switch row.kind {
		case rowKindRepoHeader:
			line = m.styles.SectionLabel.Render(row.repoName)
		case rowKindFileHeader:
			label := row.filePath
			if label == "" {
				label = "General"
			}
			line = "  " + m.styles.Subtitle.Render(label)
		case rowKindComment:
			c, ok := m.findComment(row.itemID, row.commentID)
			if !ok {
				line = m.styles.Muted.Render("    [missing comment]")
				break
			}
			mark := "[ ]"
			if m.commentSelected[row.commentID] {
				mark = "[x]"
			}
			locator := ""
			if c.Line > 0 {
				locator = fmt.Sprintf(":%d ", c.Line)
			}
			body := strings.TrimSpace(strings.SplitN(c.Body, "\n", 2)[0])
			text := fmt.Sprintf("    %s %s%s — %s", mark, locator, c.ReviewerLogin, body)
			if i == m.selectorCursor {
				text = m.styles.Active.Render("  ▶ " + strings.TrimLeft(text, " "))
			}
			line = text
		}
		out = append(out, ansi.Truncate(line, width, "…"))
	}
	return strings.Join(out, "\n")
}

func (m ReviewFollowupModel) renderPreview(width, height int) string {
	row := m.currentRow()
	if row == nil || row.kind != rowKindComment {
		return m.styles.Muted.Render("Select a comment to preview")
	}
	c, ok := m.findComment(row.itemID, row.commentID)
	if !ok {
		return m.styles.Muted.Render("Comment unavailable")
	}
	header := m.styles.Title.Render(c.ReviewerLogin)
	if !c.CreatedAt.IsZero() {
		header += m.styles.Muted.Render("  " + c.CreatedAt.Format("2006-01-02 15:04"))
	}
	parts := []string{
		ansi.Truncate(header, width, ""),
		components.RenderOverlayDivider(m.styles, width),
	}
	if c.Path != "" {
		loc := c.Path
		if c.Line > 0 {
			loc = fmt.Sprintf("%s:%d", c.Path, c.Line)
		}
		parts = append(parts, ansi.Truncate(m.styles.Muted.Render(loc), width, ""))
	}
	parts = append(parts, "")
	body := strings.TrimRight(c.Body, "\n")
	for _, line := range strings.Split(body, "\n") {
		parts = append(parts, ansi.Truncate(line, width, ""))
	}
	if c.URL != "" {
		parts = append(parts, "")
		parts = append(parts, ansi.Truncate(m.styles.Muted.Render(c.URL), width, ""))
	}
	if len(parts) > height {
		parts = parts[:height]
	}
	return strings.Join(parts, "\n")
}

func (m ReviewFollowupModel) viewConfirm() string {
	fw := m.frameWidth()
	iw := m.innerWidth()
	header := []string{
		m.styles.Title.Render("Re-plan from review feedback"),
		components.RenderOverlayDivider(m.styles, iw),
	}
	body := strings.Join([]string{
		"This will discard the current plan and create a new one based on the",
		"selected review comments.",
		"",
		"Affected:",
		"  • Plan will be replaced",
		"  • PR descriptions will be updated to the new plan when approved",
		"  • Implementation results from the previous plan are no longer the active workflow",
		"",
		"Continue?",
	}, "\n")
	body = fitViewBox(body, iw, m.frameHeight())
	footer := m.styles.Hint.Render(ansi.Truncate("[y] Yes, re-plan    [n/Esc] Cancel", iw, ""))
	return components.RenderOverlayFrame(m.styles, fw, components.OverlayFrameSpec{
		HeaderLines: header,
		Body:        body,
		Footer:      footer,
		Focused:     true,
	})
}

// --- selector row management ---

func (m *ReviewFollowupModel) rebuildSelectorRows() {
	rows := make([]reviewSelectorRow, 0, 32)
	for _, it := range m.scopedItems {
		comments := m.commentsByItem[it.ID]
		if len(comments) == 0 {
			continue
		}
		rows = append(rows, reviewSelectorRow{
			kind:     rowKindRepoHeader,
			itemID:   it.ID,
			repoName: it.RepoName,
		})
		// Group by file with general first.
		var general []adapter.ReviewComment
		byFile := make(map[string][]adapter.ReviewComment)
		for _, c := range comments {
			if strings.TrimSpace(c.Path) == "" {
				general = append(general, c)
				continue
			}
			byFile[c.Path] = append(byFile[c.Path], c)
		}
		if len(general) > 0 {
			rows = append(rows, reviewSelectorRow{
				kind:   rowKindFileHeader,
				itemID: it.ID,
			})
			for _, c := range general {
				rows = append(rows, reviewSelectorRow{
					kind:      rowKindComment,
					itemID:    it.ID,
					commentID: c.ID,
				})
			}
		}
		filePaths := make([]string, 0, len(byFile))
		for p := range byFile {
			filePaths = append(filePaths, p)
		}
		sort.Strings(filePaths)
		for _, p := range filePaths {
			rows = append(rows, reviewSelectorRow{
				kind:     rowKindFileHeader,
				itemID:   it.ID,
				filePath: p,
			})
			for _, c := range byFile[p] {
				rows = append(rows, reviewSelectorRow{
					kind:      rowKindComment,
					itemID:    it.ID,
					filePath:  p,
					commentID: c.ID,
				})
			}
		}
	}
	m.selectorRows = rows
}

func (m ReviewFollowupModel) firstCommentRow() int {
	for i, r := range m.selectorRows {
		if r.kind == rowKindComment {
			return i
		}
	}
	return 0
}

func (m ReviewFollowupModel) currentRow() *reviewSelectorRow {
	if m.selectorCursor < 0 || m.selectorCursor >= len(m.selectorRows) {
		return nil
	}
	r := m.selectorRows[m.selectorCursor]
	return &r
}

// findComment looks up a comment by item/id and reports whether it was found.
// Returns the zero value with ok=false when the row is desynced from
// commentsByItem (e.g. mid-MergeRefetch); callers MUST check ok before rendering.
func (m ReviewFollowupModel) findComment(itemID, commentID string) (adapter.ReviewComment, bool) {
	for _, c := range m.commentsByItem[itemID] {
		if c.ID == commentID {
			return c, true
		}
	}
	return adapter.ReviewComment{}, false
}

// toggleAtCursor toggles the focused row. For comment rows, flips that comment.
// For header rows, cascades to all child comments (sets all to the inverse of the
// majority current state — if any unselected, select all; otherwise deselect all).
func (m *ReviewFollowupModel) toggleAtCursor() {
	row := m.currentRow()
	if row == nil {
		return
	}
	switch row.kind {
	case rowKindComment:
		m.commentSelected[row.commentID] = !m.commentSelected[row.commentID]
	case rowKindRepoHeader:
		m.toggleGroup(row.itemID, "", true)
	case rowKindFileHeader:
		m.toggleGroup(row.itemID, row.filePath, false)
	}
}

// toggleGroup toggles every comment in the named group. anyFile=true means all files
// for the repo; anyFile=false means only the given filePath.
func (m *ReviewFollowupModel) toggleGroup(itemID, filePath string, allFiles bool) {
	var ids []string
	for _, c := range m.commentsByItem[itemID] {
		matches := allFiles || c.Path == filePath || (filePath == "" && c.Path == "")
		if !matches {
			continue
		}
		ids = append(ids, c.ID)
	}
	if len(ids) == 0 {
		return
	}
	// Determine target state: if any is unselected, select all; else deselect all.
	target := false
	for _, id := range ids {
		if !m.commentSelected[id] {
			target = true
			break
		}
	}
	for _, id := range ids {
		m.commentSelected[id] = target
	}
}

// selectAll sets every comment in scope to selected.
func (m *ReviewFollowupModel) selectAll() {
	for _, it := range m.scopedItems {
		for _, c := range m.commentsByItem[it.ID] {
			m.commentSelected[c.ID] = true
		}
	}
}

// selectNone clears every comment selection in scope.
func (m *ReviewFollowupModel) selectNone() {
	for _, it := range m.scopedItems {
		for _, c := range m.commentsByItem[it.ID] {
			m.commentSelected[c.ID] = false
		}
	}
}

// applyPickerSelection promotes the picker's checked PRs to scopedItems and moves
// to the selector stage. Comments belonging to unchecked PRs are wiped from selection.
func (m *ReviewFollowupModel) applyPickerSelection() {
	scoped := make([]ArtifactItem, 0, len(m.pickerItems))
	for _, it := range m.pickerItems {
		if m.pickerSelected[it.ID] {
			scoped = append(scoped, it)
		}
	}
	m.scopedItems = scoped
	// Drop selection state for PRs no longer in scope.
	keep := make(map[string]bool)
	for _, it := range scoped {
		for _, c := range m.commentsByItem[it.ID] {
			keep[c.ID] = true
		}
	}
	for id := range m.commentSelected {
		if !keep[id] {
			delete(m.commentSelected, id)
		}
	}
	m.stage = reviewFollowupStageSelector
	m.rebuildSelectorRows()
	m.selectorCursor = m.firstCommentRow()
}

// --- Update ---

// ReviewFollowupCancelMsg is emitted when the user cancels the overlay before dispatch.
// The app handles it by closing the overlay.
type ReviewFollowupCancelMsg struct{}

// ReviewFollowupRefetchMsg is emitted when dispatch hits the staleness window and a
// silent re-fetch is required before the address/replan message is sent.
//
// Mode is either "address" or "replan" so the app knows which dispatcher to invoke
// once the fresh data arrives.
type ReviewFollowupRefetchMsg struct {
	WorkItemID string
	Items      []ArtifactItem
	Mode       string
}

// Update handles overlay input and lifecycle messages.
func (m ReviewFollowupModel) Update(msg tea.Msg) (ReviewFollowupModel, tea.Cmd) {
	if !m.active {
		return m, nil
	}
	switch msg := msg.(type) {
	case spinner.TickMsg:
		// Drop tick messages once the loading stage ends; otherwise the spinner
		// keeps requesting fresh ticks for the lifetime of the overlay.
		if m.stage != reviewFollowupStageLoading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m ReviewFollowupModel) handleKey(msg tea.KeyMsg) (ReviewFollowupModel, tea.Cmd) {
	switch m.stage {
	case reviewFollowupStageLoading:
		if msg.String() == keyEsc {
			return m, func() tea.Msg { return ReviewFollowupCancelMsg{} }
		}
	case reviewFollowupStagePicker:
		return m.handleKeyPicker(msg)
	case reviewFollowupStageSelector:
		return m.handleKeySelector(msg)
	case reviewFollowupStageConfirm:
		return m.handleKeyConfirm(msg)
	}
	return m, nil
}

func (m ReviewFollowupModel) handleKeyPicker(msg tea.KeyMsg) (ReviewFollowupModel, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		return m, func() tea.Msg { return ReviewFollowupCancelMsg{} }
	case "up", "k":
		if m.pickerCursor > 0 {
			m.pickerCursor--
		}
	case "down", "j":
		if m.pickerCursor < len(m.pickerItems)-1 {
			m.pickerCursor++
		}
	case " ":
		if m.pickerCursor < len(m.pickerItems) {
			id := m.pickerItems[m.pickerCursor].ID
			m.pickerSelected[id] = !m.pickerSelected[id]
		}
	case "a":
		for _, it := range m.pickerItems {
			m.pickerSelected[it.ID] = true
		}
	case "n":
		for _, it := range m.pickerItems {
			m.pickerSelected[it.ID] = false
		}
	case keyEnter:
		if !m.pickerHasAnySelected() {
			return m, nil
		}
		m.applyPickerSelection()
	}
	return m, nil
}

func (m ReviewFollowupModel) pickerHasAnySelected() bool {
	for _, it := range m.pickerItems {
		if m.pickerSelected[it.ID] {
			return true
		}
	}
	return false
}

func (m ReviewFollowupModel) handleKeySelector(msg tea.KeyMsg) (ReviewFollowupModel, tea.Cmd) {
	switch msg.String() {
	case keyEsc:
		return m, func() tea.Msg { return ReviewFollowupCancelMsg{} }
	case "up", "k":
		if m.selectorCursor > 0 {
			m.selectorCursor--
		}
	case "down", "j":
		if m.selectorCursor < len(m.selectorRows)-1 {
			m.selectorCursor++
		}
	case " ":
		m.toggleAtCursor()
	case "a":
		m.selectAll()
	case "n":
		m.selectNone()
	case keyEnter:
		if !m.HasAnySelection() {
			return m, nil
		}
		cmd := m.dispatchAddress()
		return m, cmd
	case "p":
		if !m.HasAnySelection() {
			return m, nil
		}
		m.stage = reviewFollowupStageConfirm
	}
	return m, nil
}

func (m ReviewFollowupModel) handleKeyConfirm(msg tea.KeyMsg) (ReviewFollowupModel, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		cmd := m.dispatchReplan()
		return m, cmd
	case "n", "N", keyEsc:
		m.stage = reviewFollowupStageSelector
	}
	return m, nil
}

// dispatchAddress emits a ReviewFollowupRefetchMsg if the data is stale, otherwise
// a FollowUpFromReviewAddressMsg with the per-repo formatted feedback. When stale,
// the overlay also moves to the loading stage so input is gated and the spinner
// resumes — preventing duplicate dispatches while the refetch is in flight.
func (m *ReviewFollowupModel) dispatchAddress() tea.Cmd {
	if m.IsStale(time.Now()) {
		m.stage = reviewFollowupStageLoading
		workItemID := m.workItemID
		items := m.items
		return tea.Batch(
			m.spinner.Tick,
			func() tea.Msg {
				return ReviewFollowupRefetchMsg{WorkItemID: workItemID, Items: items, Mode: "address"}
			},
		)
	}
	perRepo := m.FormatPerRepo()
	workItemID := m.workItemID
	return func() tea.Msg {
		return FollowUpFromReviewAddressMsg{WorkItemID: workItemID, PerRepo: perRepo}
	}
}

// dispatchReplan emits a ReviewFollowupRefetchMsg if stale, else a single replan msg.
// When stale, transitions to loading to gate further input.
func (m *ReviewFollowupModel) dispatchReplan() tea.Cmd {
	if m.IsStale(time.Now()) {
		m.stage = reviewFollowupStageLoading
		workItemID := m.workItemID
		items := m.items
		return tea.Batch(
			m.spinner.Tick,
			func() tea.Msg {
				return ReviewFollowupRefetchMsg{WorkItemID: workItemID, Items: items, Mode: "replan"}
			},
		)
	}
	feedback := m.FormatAllSelected()
	workItemID := m.workItemID
	return func() tea.Msg {
		return FollowUpFromReviewReplanMsg{WorkItemID: workItemID, Feedback: feedback}
	}
}

// --- helpers ---

func padRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	pw := ansi.StringWidth(s)
	if pw >= w {
		return s
	}
	return s + strings.Repeat(" ", w-pw)
}
