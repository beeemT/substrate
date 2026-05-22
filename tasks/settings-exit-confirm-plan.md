# Plan: Settings Exit Confirmation Modal

## Problem
The settings view currently requires explicit user action `[s]` to save changes. The user wants to change this to:
- Auto-track dirty state (already exists as `m.dirty`)
- On exit (`esc`), if dirty, show a confirmation modal
- Modal behavior: `enter` => save, `esc` => discard, `y`/`n` also work, focus Yes by default

## Current Behavior

### Key Bindings (line 1054-1055 in settings_page.go)
```go
case "s":
    return m.returnWithSyncedMainViewport(m.applyCmd(svcs))
```

### Footer Hint (line 1616)
```
[↑↓] navigate tree  [→] expand/open  [←] collapse/up  [enter] focus settings  [esc] close  [s] save  [t] test  [r] reveal
```

### Esc Handling (line 1060-1065)
```go
case "esc":
    if m.fieldsFocused() {
        m.focusSections()
        return m.returnWithSyncedMainViewport(nil)
    }
    return m.returnWithSyncedMainViewport(func() tea.Msg { return CloseOverlayMsg{} })
```

### Dirty State
- Tracked via `m.dirty bool` (line 102)
- Set to `true` on field edit commit (line 320) and bool toggle (line 1049)
- Preserved across refresh via `wasDirty` (line 1082, 1094)
- Reset to `false` in `SetSnapshot` (line 159)

## Changes Required

### 1. Add confirm modal state to `SettingsPage` struct

First, add key constants to the file-level constants (near line 34):
```go
const (
    settingHarness = "harness"
    settingFalse   = "false"
    providerLinear = "linear"
    keyEsc         = "esc"   // ADD
    keyEnter       = "enter" // ADD
)
```

Then add fields to `SettingsPage` struct:
```go
type SettingsPage struct {
    // ... existing fields ...
    
    // Modal state for exit confirmation
    confirmModalOpen bool
    closeAfterSave   bool  // true = close overlay after save completes
}
```

**Note**: `confirmFocusYes` is not needed—Yes is always focused by default, Enter always saves, Esc always discards.

### 2. Add exit confirmation modal methods

```go
// renderConfirmExitModal renders the exit confirmation dialog when dirty
func (m SettingsPage) renderConfirmExitModal() string {
    // Pattern similar to viewConfirm() in overlay_review_followup.go:702-728
    // Header: "Unsaved Changes"
    // Body: "You have unsaved changes. Do you want to save before closing?"
    // Footer: "[Enter] Save  [y] Yes  [n/Esc] Discard"
    // Note: user requested NOT to visualize esc action, but we show it for [n/Esc]
}

// confirmModalFooterText returns keyboard hints for the confirm modal
func (m SettingsPage) confirmModalFooterText() string {
    return "[Enter/y] Save  [n/Esc] Discard"
}

// confirmModalKeyHandler handles keyboard input while confirm modal is open
func (m *SettingsPage) confirmModalKeyHandler(msg tea.KeyMsg, svcs Services) (SettingsPage, tea.Cmd) {
    switch msg.String() {
    case "y", keyEnter:
        // Save and close
        m.confirmModalOpen = false
        m.closeAfterSave = true
        return m.returnWithSyncedMainViewport(m.applyCmd(svcs))
    case "n", keyEsc:
        // Discard and close
        m.confirmModalOpen = false
        m.dirty = false
        return m.returnWithSyncedMainViewport(func() tea.Msg { return CloseOverlayMsg{} })
    }
    return m, nil
}
```

**Note**: Arrow key navigation (left/right/h/l) is unnecessary complexity—Yes is focused by default, Enter executes save, Esc/n discards.

### 3. Update `Update()` to handle confirm modal

```go
func (m SettingsPage) Update(msg tea.Msg, svcs Services) (SettingsPage, tea.Cmd) {
    // ... existing code ...
    
    switch msg := msg.(type) {
    case tea.KeyMsg:
        // Handle confirm modal first if open
        if m.confirmModalOpen {
            return m.confirmModalKeyHandler(msg, svcs)
        }
        
        if m.editing {
            return m.returnWithSyncedMainViewport(m.updateFieldEditor(msg))
        }
        
        switch msg.String() {
        // ... existing key handling ...
        
        case "esc":
            if m.fieldsFocused() {
                m.focusSections()
                return m.returnWithSyncedMainViewport(nil)
            }
            // NEW: Check dirty state before closing
            if m.dirty {
                m.confirmModalOpen = true
                return m.returnWithSyncedMainViewport(nil)
            }
            return m.returnWithSyncedMainViewport(func() tea.Msg { return CloseOverlayMsg{} })
            
        // REMOVED: case "s": save shortcut - no longer needed
        }
    }
}
```

### 4. Update `View()` to render confirm modal overlay

The confirm modal should render before the edit modal:

```go
func (m SettingsPage) View() string {
    // Render confirm modal if open (renders on top of everything else)
    if m.confirmModalOpen {
        return m.renderConfirmExitModal()
    }
    
    // Render edit modal if open
    if m.editing {
        return m.renderEditModal()
    }
    
    // ... existing return ...
}
```

### 5. Update `footerText()` to remove [s] save hint

Three locations need `[s] save` removed:

```go
func (m SettingsPage) footerText() string {
    hint := "[↑↓] navigate tree  [→] expand/open  [←] collapse/up  [enter] focus settings  [esc] close  [t] test  [r] reveal"  // line 1616: remove [s] save
    if providerSupportsLogin(providerForSection(m.currentSection())) {
        hint = "[↑↓] navigate tree  [→] expand/open  [←] collapse/up  [enter] focus settings  [esc] close  [t] test  [g] login  [r] reveal"  // line 1618: remove [s] save
    }
    if m.editing {
        hint = "[enter] save edit  [esc] cancel edit"
    } else if m.fieldsFocused() {
        hint = "[↑↓] settings  [enter/e] edit  [space] toggle bool  [left/esc] groups  [t] test  [r] reveal"  // line 1623: remove [s] save
        if providerSupportsLogin(providerForSection(m.currentSection())) {
            hint = "[↑↓] settings  [enter/e] edit  [space] toggle bool  [left/esc] groups  [t] test  [g] login  [r] reveal"  // line 1625: remove [s] save
        }
    }
    // ... rest unchanged ...
}
```

### 6. Update `KeybindHints()` if exists

Search for and update any other places showing `[s] save` in hints.

### 7. Update `SettingsAppliedMsg` handling

When `SettingsAppliedMsg` is received, if we came from the confirm modal, we should close the overlay:

```go
case SettingsAppliedMsg:
    m.statusText = msg.Message
    m.errorText = ""
    // If confirm modal was open and we're saving, close overlay
    if m.confirmModalOpen {
        m.confirmModalOpen = false
        m.dirty = false
        return m.returnWithSyncedMainViewport(func() tea.Msg { return CloseOverlayMsg{} })
    }
    m.RefreshFromService()
```

Actually, this could cause issues. Let me reconsider...

The `applyCmd` already emits `SettingsAppliedMsg`. We need the save to complete first, then close. We should probably modify the flow:

1. User presses esc with dirty state
2. Confirm modal opens
3. User presses enter/y
4. We save (via `applyCmd`)
5. `SettingsAppliedMsg` is received
6. We close the overlay

The current implementation of `applyCmd`:
```go
func (m SettingsPage) applyCmd(svcs Services) tea.Cmd {
    return func() tea.Msg {
        result, err := m.service.Save(context.Background(), m.sections, svcs)
        if err != nil {
            return ErrMsg{Err: err}
        }
        return SettingsAppliedMsg{Reload: result.Services, Message: result.Message}
    }
}
```

And `SettingsAppliedMsg` handling currently does:
```go
case SettingsAppliedMsg:
    m.statusText = msg.Message
    m.errorText = ""
    m.RefreshFromService()
```

So we need to add a flag to track "close after save":
```go
type SettingsPage struct {
    // ...
    confirmModalOpen bool
    closeAfterSave   bool  // NEW: close overlay after successful save
}
```

And update `confirmModalKeyHandler`:
```go
case "y", keyEnter:
    m.confirmModalOpen = false
    m.closeAfterSave = true  // Signal to close after save completes
    return m.returnWithSyncedMainViewport(m.applyCmd(svcs))
```

And update `SettingsAppliedMsg`:
```go
case SettingsAppliedMsg:
    m.statusText = msg.Message
    m.errorText = ""
    m.RefreshFromService()
    if m.closeAfterSave {
        m.closeAfterSave = false
        m.dirty = false
        return m.returnWithSyncedMainViewport(func() tea.Msg { return CloseOverlayMsg{} })
    }
```

### 8. Close the modal when settings page is externally closed

In `Close()` method, reset the modal state:
```go
func (m *SettingsPage) Close() {
    m.active = false
    m.confirmModalOpen = false  // Reset
    m.closeAfterSave = false     // Reset
    // ... existing code ...
}
```

## Files to Modify

1. `internal/tui/views/settings_page.go`
   - Add `keyEsc` and `keyEnter` constants to file-level constants
   - Add `confirmModalOpen` and `closeAfterSave` fields to struct
   - Add `renderConfirmExitModal()` method
   - Add `confirmModalFooterText()` method
   - Add `confirmModalKeyHandler()` method
   - Update `Update()` to handle confirm modal and dirty exit
   - Update `View()` to render confirm modal (before edit modal)
   - Update `footerText()` to remove `[s] save` from all 4 locations
   - Update `SettingsAppliedMsg` handling to close after save
   - Update `ErrMsg` handling to reset `closeAfterSave` on error
   - Update `Close()` to reset modal state
   - Remove `case "s":` keybinding

2. `internal/tui/views/settings_page_test.go`
   - Add tests for:
     - `esc` with dirty state opens confirm modal
     - `esc` without dirty state closes overlay immediately
     - `enter` with confirm modal open saves and closes
     - `y` with confirm modal open saves and closes
     - `n` with confirm modal open discards and closes
     - `esc` with confirm modal open discards and closes
     - Footer text doesn't show `[s] save`
   - **Note**: The test mock `testSettingsService.Save` currently returns an error. Tests for save-and-close flow will need a mock that returns `SettingsApplyResult{}` on success.

3. `internal/tui/views/app_settings_test.go`
   - Update `TestApp_EscClosesSettingsOverlay` to test dirty state behavior

## Visual Design

The confirm modal should use the existing overlay frame pattern (same as `renderEditModal()`):

```go
func (m SettingsPage) renderConfirmExitModal() string {
    frameWidth := min(96, max(24, m.width-4))
    if m.width > 0 {
        frameWidth = min(frameWidth, m.width)
    }
    contentWidth := m.styles.Chrome.OverlayFrame.InnerWidth(max(1, frameWidth))
    
    header := []string{
        m.styles.Title.Render("Unsaved Changes"),
    }
    body := "You have unsaved changes. Do you want to save before closing?"
    footer := m.styles.Hint.Render(truncate(m.confirmModalFooterText(), contentWidth))
    
    return components.RenderOverlayFrame(m.styles, frameWidth, components.OverlayFrameSpec{
        HeaderLines: header,
        Body:        body,
        Footer:      footer,
        Focused:     true,
    })
}
```

Key elements:
- Header: "Unsaved Changes"
- Body: "You have unsaved changes. Do you want to save before closing?"
- Footer: `[Enter/y] Save  [n/Esc] Discard`
- Yes (Save) is focused by default—Enter always saves, Esc always discards

## Testing Strategy

1. Unit tests in `settings_page_test.go`:
   - Test esc key with dirty=true opens modal
   - Test esc key with dirty=false closes overlay
   - Test enter key with modal open triggers save
   - Test y key with modal open triggers save
   - Test n key with modal open discards and closes
   - Test esc key with modal open discards and closes
   - Test footer doesn't show [s] hint
   - Test save success closes overlay after modal
   - Test save error keeps modal state

2. Integration test in `app_settings_test.go`:
   - Full flow: open settings → edit → esc → confirm → save/close

## Error Handling

On save failure (`ErrMsg`), if `closeAfterSave` was set:

```go
case ErrMsg:
    m.errorText = msg.Err.Error()
    m.closeAfterSave = false  // User stays in settings, can retry
```

## Risks & Considerations

1. **Race condition**: If save fails (network, config error), user stays in settings overlay with error displayed. `closeAfterSave` must be reset on error so user can retry or close without the save-closing flow re-triggering.

2. **Multiple edits during save**: User could edit another field while save is in progress. This is handled by current implementation (single save operation).

3. **Backwards compatibility**: Users who muscle-memory `[s]` to save will need to adapt. Consider showing a toast notification on first use: "Changes are now saved automatically on exit" or similar educational message.

4. **Dirty state after login**: `SettingsLoginCompletedMsg` preserves dirty state (`m.dirty = wasDirty || msg.Dirty`). This is correct - login shouldn't clear unsaved changes.
