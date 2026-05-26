# TUI Overlay Review Assignment

## Files to Review
1. `internal/tui/views/overlay_new_session.go` - Browse spinner sequence handling
2. `internal/tui/views/app_browse_test.go` - Tests for spinner tick behavior
3. `.github/workflows/release.yml` - CI/CD workflow changes

## Key Changes Summary

### TUI Overlay Changes
The change adds a sequence number to `browseSpinnerTickMsg` to prevent stale spinner ticks from interfering with new loading cycles. Key changes:
1. Added `seq int` field to `browseSpinnerTickMsg` struct
2. Added `browseSpinnerSeq int` field to `NewSessionOverlay` struct
3. `startBrowseLoading()` increments the sequence counter
4. `browseSpinnerTickCmd()` and `browseSpinnerIntervalTickCmd()` now carry the sequence
5. Update handler ignores ticks where `msg.seq != m.browseSpinnerSeq`

### Workflow Changes
- Added second `bun install` step for `bridge/foreman-mcp` subdirectory
- Added `bun build` step for `foreman-mcp` binary

## Review Focus
- Is the sequence-based deduplication logic correct?
- Are there race conditions or edge cases?
- Do the tests adequately cover the stale tick scenario?
- Is the workflow change correct (is the path `bridge/foreman-mcp` correct after rename)?

Use the diffs provided in the task description (NEVER re-run git diff).

## Diff for internal/tui/views/overlay_new_session.go
```diff
@@ -136,7 +136,7 @@ const browseSpinnerInterval = 100 * time.Millisecond
 // fires a network reload; earlier ticks are silently dropped.
 type (
 	browseDebounceMsg    struct{ seq int }
-	browseSpinnerTickMsg struct{}
+	browseSpinnerTickMsg struct{ seq int }
 	browsePageState      struct {
 		Items      []adapter.ListItem
 		Offset     int
@@ -287,6 +287,7 @@ type NewSessionOverlay struct { //nolint:recvcheck // Bubble Tea: Update returns
 		loading                        bool
 		browseSpinnerFrame             int
 		browseSpinnerVisible           bool
+		browseSpinnerSeq               int
 		hasMore                        bool
 		manualTitle                    components.GrowingTextInput
 		manualDesc                     components.GrowingTextArea
@@ -1106,16 +1107,28 @@ func (m *NewSessionOverlay) nextRequestID() int {
 	return m.requestSeq
 }

-func browseSpinnerTickCmd() tea.Cmd {
-	return func() tea.Msg { return browseSpinnerTickMsg{} }
+func browseSpinnerTickCmd(seq int) tea.Cmd {
+	return func() tea.Msg { return browseSpinnerTickMsg{seq: seq} }
 }

-func (m *NewSessionOverlay) reloadItems() tea.Cmd {
+func browseSpinnerIntervalTickCmd(seq int) tea.Cmd {
+	return tea.Tick(browseSpinnerInterval, func(time.Time) tea.Msg {
+		return browseSpinnerTickMsg{seq: seq}
+	})
+}
+
+func (m *NewSessionOverlay) startBrowseLoading() int {
 	m.loading = true
 	m.browseSpinnerFrame = 0
 	m.browseSpinnerVisible = false
+	m.browseSpinnerSeq++
+	return m.browseSpinnerSeq
+}{
+
+func (m *NewSessionOverlay) reloadItems() tea.Cmd {
+	spinnerSeq := m.startBrowseLoading()

-	return tea.Batch(m.loadItemsCmd(browseLoadReset, m.nextRequestID()), browseSpinnerTickCmd())
+	return tea.Batch(m.loadItemsCmd(browseLoadReset, m.nextRequestID()), browseSpinnerTickCmd(spinnerSeq))
 }

 func (m *NewSessionOverlay) SetSavedNewSessionFilters(filters []domain.NewSessionFilter) {
@@ -1313,10 +1326,8 @@ func (m *NewSessionOverlay) loadMoreItems() tea.Cmd {
 	if m.loading || !m.hasMore {
 		return nil
 	}
-	m.loading = true
-	m.browseSpinnerFrame = 0
-	m.browseSpinnerVisible = false
-	return tea.Batch(m.loadItemsCmd(browseLoadAppend, m.nextRequestID()), browseSpinnerTickCmd())
+	spinnerSeq := m.startBrowseLoading()
+	return tea.Batch(m.loadItemsCmd(browseLoadAppend, m.nextRequestID()), browseSpinnerTickCmd(spinnerSeq))
 }

 func (m *NewSessionOverlay) cycleProvider(delta int) tea.Cmd {
@@ -2044,11 +2055,12 @@ func (m NewSessionOverlay) Update(msg tea.Msg) (NewSessionOverlay, tea.Cmd) {
 			m.browseSpinnerVisible = false
 			return m, nil
 		}
+		if msg.seq != m.browseSpinnerSeq {
+			return m, nil
+		}
 		m.browseSpinnerVisible = true
 		m.browseSpinnerFrame = (m.browseSpinnerFrame + 1) % len(browseSpinnerFrames)
-		return m, tea.Tick(browseSpinnerInterval, func(time.Time) tea.Msg {
-			return browseSpinnerTickMsg{}
-		})
+		return m, browseSpinnerIntervalTickCmd(msg.seq)

 	case issueListLoadedMsg:
 		if msg.requestID != 0 && msg.requestID != m.requestSeq {
```

## Diff for internal/tui/views/app_browse_test.go
```diff
@@ -292,7 +292,7 @@ func TestNewSessionOverlaySpinnerOnlyLoadingState(t *testing.T) {
 		t.Fatal("expected overlay to enter loading state")
 	}

-	overlay, _ = overlay.Update(browseSpinnerTickMsg{})
+	overlay, _ = overlay.Update(browseSpinnerTickMsg{seq: overlay.browseSpinnerSeq})
 	if !overlay.browseSpinnerVisible {
 		t.Fatal("expected spinner to become visible while loading")
 	}
@@ -313,6 +313,55 @@ func TestNewSessionOverlaySpinnerOnlyLoadingState(t *testing.T) {
 	}
 }

+func TestNewSessionOverlayIgnoresStaleSpinnerTickAfterReloadRestart(t *testing.T) {
+	t.Parallel()
+
+	githubAdapter := &browseTestAdapter{name: "github", browseScopes: []domain.SelectionScope{domain.ScopeIssues}, browseFilters: map[domain.SelectionScope]adapter.BrowseFilterCapabilities{domain.ScopeIssues: {Views: []string{"assigned_to_me", "all"}}}}
+	overlay := NewNewSessionOverlay([]adapter.WorkItemAdapter{githubAdapter}, "ws-1", styles.NewStyles(styles.DefaultTheme))
+	overlay.SetSize(100, 30)
+	firstCmd := overlay.Open()
+	if firstCmd == nil {
+		t.Fatal("expected initial open to start browse loading")
+	}
+	firstSeq := overlay.browseSpinnerSeq
+	if firstSeq == 0 {
+		t.Fatal("expected first loading cycle to set spinner seq")
+	}
+
+	secondCmd := overlay.reloadItems()
+	if secondCmd == nil {
+		t.Fatal("expected reload restart to return command")
+	}
+	secondSeq := overlay.browseSpinnerSeq
+	if secondSeq == firstSeq {
+		t.Fatalf("spinner seq did not advance on reload restart: %d", secondSeq)
+	}
+
+	updated, cmd := overlay.Update(browseSpinnerTickMsg{seq: firstSeq})
+	if cmd != nil {
+		t.Fatal("stale spinner tick must not schedule a follow-up tick")
+	}
+	if updated.browseSpinnerVisible || updated.browseSpinnerFrame != 0 {
+		t.Fatalf("stale tick changed spinner visible=%v frame=%d", updated.browseSpinnerVisible, updated.browseSpinnerFrame)
+	}
+
+	updated, cmd = updated.Update(browseSpinnerTickMsg{seq: secondSeq})
+	if cmd == nil {
+		t.Fatal("current spinner tick must schedule a follow-up tick")
+	}
+	if !updated.browseSpinnerVisible || updated.browseSpinnerFrame != 1 {
+		t.Fatalf("current tick visible=%v frame=%d, want visible frame 1", updated.browseSpinnerVisible, updated.browseSpinnerFrame)
+	}
+
+	restarted, cmd := updated.Update(browseSpinnerTickMsg{seq: firstSeq})
+	if cmd != nil {
+		t.Fatal("stale spinner tick after current tick must not schedule a follow-up tick")
+	}
+	if restarted.browseSpinnerFrame != updated.browseSpinnerFrame {
+		t.Fatalf("stale tick advanced frame from %d to %d", updated.browseSpinnerFrame, restarted.browseSpinnerFrame)
+	}
+}
+
 func TestNewSessionOverlayCachedReopenRestoresResultsAndCursor(t *testing.T) {
 	t.Parallel()
```

## Diff for .github/workflows/release.yml
```diff
@@ -78,7 +78,9 @@ jobs:
             bun-${{ runner.os }}-

       - name: Install Bun dependencies
-        run: |
+        run: |
           bun install --cwd bridge --frozen-lockfile
+          bun install --cwd bridge/foreman-mcp --frozen-lockfile

       - name: Build
         env:
@@ -104,6 +106,11 @@ jobs:
             --target "${{ matrix.bridge_target }}" \
             --outfile "dist/bridge/claude-agent-bridge"

+          bun build bridge/foreman-mcp/index.ts \
+            --compile \
+            --target "${{ matrix.bridge_target }}" \
+            --outfile "dist/bridge/foreman-mcp"
+
           cp "bridge/node_modules/@oh-my-pi/pi-natives/native/${{ matrix.native_addon }}" "dist/bridge/${{ matrix.native_addon }}"

           tar -C dist -czf "substrate_${VERSION_NUM}_${{ matrix.goos }}_${{ matrix.goarch }}.tar.gz" .
```

## Instructions
1. Call `report_finding` tool per issue found (severity: high/medium/low)
2. Call `yield` tool with your final verdict when done reviewing
