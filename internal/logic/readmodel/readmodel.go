// Package readmodel provides daemon-owned product read models derived from a
// coherent logic.InitialSnapshot. It exists so visualization clients do not
// have to reimplement product-level state derivation (overview cards, sidebar
// grouping, action availability) on top of the raw domain data they receive
// from the event stream and snapshot RPC.
//
// The read model is pure: every derivation takes a logic.InitialSnapshot plus
// stable identifiers and returns deterministic Go values. It has no I/O and no
// dependencies on Bubble Tea, lipgloss, or any view-layer package. Clients
// (TUI today, future Electron renderer) consume the result and are free to
// render it however they like.
package readmodel

import (
	"sort"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/logic"
)

// ActionKind identifies the product-level action a card represents. It
// mirrors the constants used by the TUI's OverviewActionKind so derived
// behavior matches the existing UX, but it is intentionally a string so
// values round-trip through JSON and proto without losing identity.
type ActionKind string

const (
	ActionPlanReview  ActionKind = "plan_review"
	ActionQuestion    ActionKind = "question"
	ActionInterrupted ActionKind = "interrupted"
	ActionReviewing   ActionKind = "reviewing"
	ActionFailed      ActionKind = "failed"
	ActionFinalize    ActionKind = "finalize"
	ActionCompleted   ActionKind = "completed"
	ActionMerged      ActionKind = "merged"
)

// SidebarKind identifies a row kind in the sidebar. It mirrors the
// SidebarEntryKind enum the TUI uses internally; the string form keeps
// the read model transport-friendly.
type SidebarKind string

const (
	SidebarWorkItem       SidebarKind = "work_item"
	SidebarTaskOverview   SidebarKind = "task_overview"
	SidebarTaskSource     SidebarKind = "task_source_details"
	SidebarTaskArtifacts  SidebarKind = "task_artifacts"
	SidebarTaskSession    SidebarKind = "task_session"
	SidebarGroupHeader    SidebarKind = "group_header"
	SidebarSessionHistory SidebarKind = "session_history"
)

// SidebarEntry is one row in the sidebar projection. Fields are stable and
// safe to render in any visualization client.
type SidebarEntry struct {
	Kind                    SidebarKind
	ID                      string
	WorkItemID              string
	SessionID               string
	ExternalID              string
	ExternalLabel           string
	Source                  string
	Title                   string
	Subtitle                string
	State                   domain.SessionState
	SessionStatus           domain.AgentSessionStatus
	RepositoryName          string
	LastActivity            time.Time
	CreatedAt               time.Time
	TotalSubPlans           int
	DoneSubPlans            int
	HasOpenQuestion         bool
	HasInterrupted          bool
	ArtifactAggregateReview string
	ArtifactAggregateCI     string
	GroupTitle              string
}

// OverviewHeader is the summary header at the top of the session overview.
type OverviewHeader struct {
	ExternalID   string
	Title        string
	StatusLabel  string
	UpdatedAt    time.Time
	ProgressText string
	Badges       []string
}

// OverviewPlan describes the plan card on the overview screen.
type OverviewPlan struct {
	StateLabel string
	Exists     bool
	Version    int
	UpdatedAt  time.Time
	RepoCount  int
	FAQCount   int
	ActionText string
}

// OverviewTaskRow is one repository task row on the overview.
type OverviewTaskRow struct {
	RepoName       string
	TaskPlanStatus string
	SessionTitle   string
	SessionStatus  string
	HarnessName    string
	UpdatedAt      time.Time
	Note           string
	SessionID      string
}

// OverviewSourceItem is a single source item for a session (provider, ref,
// title, excerpt, URL).
type OverviewSourceItem struct {
	Provider string
	Ref      string
	Title    string
	Excerpt  string
	URL      string
}

// OverviewReviewRow is one PR/MR row on the overview's external lifecycle.
type OverviewReviewRow struct {
	Kind     string
	RepoName string
	Ref      string
	URL      string
	State    string
	Branch   string
}

// OverviewExternalLifecycle is the external tracker summary for a session.
type OverviewExternalLifecycle struct {
	TrackerRefs []string
	Reviews     []OverviewReviewRow
}

// OverviewActionCard is one actionable card on the session overview. The
// fields are stable for both rendering and downstream action dispatch.
type OverviewActionCard struct {
	Kind     ActionKind
	Title    string
	Blocked  string
	Why      string
	Affected []string
	Context  []string
	CanAct   bool
	// SessionID is set for cards that act on a specific agent session
	// (interrupted, planning-was-interrupted, etc.). Empty for cards that
	// act on the work item as a whole.
	SessionID string
}

// OverviewActivityItem is one timestamped summary entry on the activity
// timeline of the overview.
type OverviewActivityItem struct {
	Summary   string
	Timestamp time.Time
}

// SessionOverview is the full overview read model for one work item.
type SessionOverview struct {
	WorkItemID string
	State      domain.SessionState
	Header     OverviewHeader
	Plan       OverviewPlan
	Tasks      []OverviewTaskRow
	Sources    []OverviewSourceItem
	Actions    []OverviewActionCard
	External   OverviewExternalLifecycle
	Activity   []OverviewActivityItem
}

// Model is the deterministic read-model engine. It holds no state of its
// own; methods are pure functions of their inputs. The zero value is ready
// to use.
type Model struct{}

// New returns a new read-model engine.
func New() Model { return Model{} }

// AvailableActions returns the ordered list of action IDs available for
// a work item. The IDs match the read-model ActionKind values (e.g.
// "plan_review", "question") and are also the stable identifiers the
// action-menu UI uses to select the right handler.
//
// This mirrors (and is intentionally a superset of) the legacy state-keyed
// switch the old readmodel exposed: state-based actions plus per-leaf
// question, interrupted, reviewing, and failed cards.
func (Model) AvailableActions(snapshot logic.InitialSnapshot, workItemID string) []ActionKind {
	actions := make([]ActionKind, 0, 4)
	overview := New().SessionOverview(snapshot, workItemID)
	seen := make(map[ActionKind]bool, len(overview.Actions))
	for _, card := range overview.Actions {
		if seen[card.Kind] {
			continue
		}
		seen[card.Kind] = true
		actions = append(actions, card.Kind)
	}
	return actions
}

// SessionOverview returns the deterministic overview for a work item. If
// the work item is not present in the snapshot the zero value is returned;
// callers can detect that with WorkItemID == "".
func (m Model) SessionOverview(snapshot logic.InitialSnapshot, workItemID string) SessionOverview {
	workItem, ok := findSession(snapshot.Sessions, workItemID)
	if !ok {
		return SessionOverview{}
	}
	plan := snapshot.Plans[workItemID]
	subPlans := snapshot.SubPlans[workItemID]
	hasPlan := plan.WorkItemID != ""
	overview := SessionOverview{
		WorkItemID: workItem.ID,
		State:      workItem.State,
		Header:     m.buildHeader(snapshot, workItem),
		Plan:       m.buildPlanView(workItem, hasPlan, plan, subPlans),
		Tasks:      m.buildTaskRows(snapshot, workItem, subPlans),
		Sources:    m.buildSources(workItem),
		Actions:    m.buildActionCards(snapshot, workItem, hasPlan, plan, subPlans),
		External:   m.buildExternalLifecycle(workItem, snapshot),
		Activity:   m.buildActivity(workItem, hasPlan, plan, snapshot),
	}
	return overview
}

// Sidebar returns the top-level session sidebar — one row per non-archived
// work item, ordered most-recently-active first. The task tree is inlined
// under a work item only when that work item is the focused one; callers
// that want every work item's tree should call TaskSidebar per work item.
// Passing an empty focusedWorkItemID returns the work-item rows alone with
// no task-tree inlining, so callers that lack a focus concept (e.g. the
// global sidebar RPC) do not pay for every work item's sub-tree.
func (m Model) Sidebar(snapshot logic.InitialSnapshot, focusedWorkItemID string) []SidebarEntry {
	workItems := append([]domain.Session(nil), snapshot.Sessions...)
	archived := make(map[string]bool, len(snapshot.ArchivedSessionIDs))
	for _, id := range snapshot.ArchivedSessionIDs {
		archived[id] = true
	}
	visible := make([]domain.Session, 0, len(workItems))
	for _, wi := range workItems {
		if archived[wi.ID] {
			continue
		}
		visible = append(visible, wi)
	}
	sort.SliceStable(visible, func(i, j int) bool {
		if !visible[i].UpdatedAt.Equal(visible[j].UpdatedAt) {
			return visible[i].UpdatedAt.After(visible[j].UpdatedAt)
		}
		return visible[i].ID < visible[j].ID
	})

	entries := make([]SidebarEntry, 0, len(visible))
	for _, wi := range visible {
		workEntry := m.buildWorkItemEntry(snapshot, wi)
		entries = append(entries, workEntry)
		// Inline the focused work item's task tree directly under its
		// work-item row so the sidebar mirrors the TUI's task view. Empty
		// focusedWorkItemID means "no focus" — skip inlining entirely.
		if focusedWorkItemID == "" || wi.ID != focusedWorkItemID {
			continue
		}
		taskEntries := m.TaskSidebar(snapshot, wi.ID)
		entries = append(entries, taskEntries...)
	}
	return entries
}

// TaskSidebar returns the sidebar rows for a single work item: the task
// overview header, the optional source-details and artifacts virtual
// nodes, and the per-kind session groups (Planning, Foreman, repository
// groups). The first entry is always a SidebarTaskOverview header; the
// rest are derived deterministically from the agent sessions for the
// work item.
func (m Model) TaskSidebar(snapshot logic.InitialSnapshot, workItemID string) []SidebarEntry {
	workItem, ok := findSession(snapshot.Sessions, workItemID)
	if !ok {
		return nil
	}
	overview := SidebarEntry{
		Kind:         SidebarTaskOverview,
		ID:           taskSidebarEntryID(workItemID, "overview"),
		WorkItemID:   workItemID,
		ExternalID:   workItem.ExternalID,
		Source:       workItem.Source,
		Title:        "Overview",
		Subtitle:     string(workItem.State),
		State:        workItem.State,
		LastActivity: workItem.UpdatedAt,
		CreatedAt:    workItem.CreatedAt,
	}
	plan := snapshot.Plans[workItemID]
	subPlans := snapshot.SubPlans[workItemID]
	if plan.WorkItemID != "" {
		overview.TotalSubPlans = len(subPlans)
		for _, sp := range subPlans {
			if sp.Status == domain.SubPlanCompleted {
				overview.DoneSubPlans++
			}
			if sp.UpdatedAt.After(overview.LastActivity) {
				overview.LastActivity = sp.UpdatedAt
			}
		}
	}
	for _, leaf := range graphLeavesForWorkItem(snapshot, workItemID) {
		if leaf.Status == domain.AgentSessionWaitingForAnswer && hasOpenQuestionForSession(snapshot, leaf.ID) {
			overview.HasOpenQuestion = true
		}
		if leaf.Status == domain.AgentSessionInterrupted {
			overview.HasInterrupted = true
		}
	}

	entries := []SidebarEntry{overview}
	if workItem.Source != "" && workItem.Source != "manual" {
		entries = append(entries, SidebarEntry{
			Kind:         SidebarTaskSource,
			ID:           taskSidebarEntryID(workItemID, "source"),
			WorkItemID:   workItemID,
			SessionID:    "source_details",
			ExternalID:   workItem.ExternalID,
			Title:        "Source details",
			LastActivity: workItem.UpdatedAt,
			CreatedAt:    workItem.CreatedAt,
		})
	}

	// Artifact aggregation drives a dedicated row when there is at least
	// one PR/MR recorded for the work item.
	reviewState, ciState, hasArtifacts := m.aggregateArtifactStates(snapshot, workItemID)
	if hasArtifacts {
		entries = append(entries, SidebarEntry{
			Kind:                    SidebarTaskArtifacts,
			ID:                      taskSidebarEntryID(workItemID, "artifacts"),
			WorkItemID:              workItemID,
			SessionID:               "artifacts",
			Title:                   "Pull requests & merge requests",
			Subtitle:                "artifacts",
			LastActivity:            workItem.UpdatedAt,
			ArtifactAggregateReview: reviewState,
			ArtifactAggregateCI:     ciState,
		})
	}

	// Group agent sessions by kind for the task tree.
	sessions := sessionsForWorkItem(snapshot, workItemID)
	planning, foreman, repos := groupAgentSessions(sessions)

	if len(planning) > 0 {
		entries = append(entries, SidebarEntry{
			Kind:       SidebarGroupHeader,
			ID:         taskSidebarEntryID(workItemID, "group:planning"),
			WorkItemID: workItemID,
			GroupTitle: "Planning",
		})
		for _, s := range planning {
			entries = append(entries, buildTaskSessionEntry(s, workItem, "Planning"))
		}
	}
	if foreman != nil {
		entries = append(entries, SidebarEntry{
			Kind:       SidebarGroupHeader,
			ID:         taskSidebarEntryID(workItemID, "group:foreman"),
			WorkItemID: workItemID,
			GroupTitle: "Foreman",
		})
		entries = append(entries, buildTaskSessionEntry(*foreman, workItem, "Foreman"))
	}
	for _, name := range sortedRepoNames(repos) {
		entries = append(entries, SidebarEntry{
			Kind:       SidebarGroupHeader,
			ID:         taskSidebarEntryID(workItemID, "group:"+name),
			WorkItemID: workItemID,
			GroupTitle: name,
		})
		for _, s := range repos[name] {
			entries = append(entries, buildTaskSessionEntry(s, workItem, ""))
		}
	}
	return entries
}

// WorkItemEntry returns a single work-item sidebar entry — useful for
// callers that want to display just the top-level row for one session
// without rendering its task tree.
func (m Model) WorkItemEntry(snapshot logic.InitialSnapshot, workItemID string) (SidebarEntry, bool) {
	workItem, ok := findSession(snapshot.Sessions, workItemID)
	if !ok {
		return SidebarEntry{}, false
	}
	return m.buildWorkItemEntry(snapshot, workItem), true
}

// buildWorkItemEntry constructs the top-level sidebar row for a work item.
func (Model) buildWorkItemEntry(snapshot logic.InitialSnapshot, workItem domain.Session) SidebarEntry {
	entry := SidebarEntry{
		Kind:         SidebarWorkItem,
		ID:           workItem.ID,
		WorkItemID:   workItem.ID,
		ExternalID:   workItem.ExternalID,
		Source:       workItem.Source,
		Title:        workItem.Title,
		Subtitle:     string(workItem.State),
		State:        workItem.State,
		LastActivity: workItem.UpdatedAt,
		CreatedAt:    workItem.CreatedAt,
	}
	if workItem.Source == "gitlab" {
		if label := gitlabExternalLabel(workItem); label != "" {
			entry.ExternalLabel = label
		}
	}
	plan := snapshot.Plans[workItem.ID]
	if plan.WorkItemID == "" {
		return entry
	}
	subPlans := snapshot.SubPlans[workItem.ID]
	entry.TotalSubPlans = len(subPlans)
	subPlanIDs := make(map[string]bool, len(subPlans))
	for _, sp := range subPlans {
		subPlanIDs[sp.ID] = true
		if sp.UpdatedAt.After(entry.LastActivity) {
			entry.LastActivity = sp.UpdatedAt
		}
		if sp.Status == domain.SubPlanCompleted {
			entry.DoneSubPlans++
		}
	}
	for _, s := range snapshot.AgentSessions {
		if s.WorkItemID != workItem.ID {
			continue
		}
		if !subPlanIDs[s.SubPlanID] {
			continue
		}
		if s.UpdatedAt.After(entry.LastActivity) {
			entry.LastActivity = s.UpdatedAt
		}
	}
	for _, leaf := range graphLeavesForWorkItem(snapshot, workItem.ID) {
		if leaf.Status == domain.AgentSessionWaitingForAnswer && hasOpenQuestionForSession(snapshot, leaf.ID) {
			entry.HasOpenQuestion = true
		}
		if leaf.Status == domain.AgentSessionInterrupted {
			entry.HasInterrupted = true
		}
	}
	return entry
}

// buildHeader mirrors buildOverviewHeader in the TUI.
func (Model) buildHeader(snapshot logic.InitialSnapshot, wi domain.Session) OverviewHeader {
	entry := New().buildWorkItemEntry(snapshot, wi)
	badges := make([]string, 0, 4)
	if wi.State == domain.SessionPlanReview {
		badges = append(badges, "waiting for approval")
	}
	if entry.HasOpenQuestion {
		badges = append(badges, "waiting for answer")
	}
	if entry.HasInterrupted {
		badges = append(badges, "interrupted")
	}
	if wi.State == domain.SessionFailed {
		badges = append(badges, "failed")
	}
	progress := ""
	if entry.TotalSubPlans > 0 {
		progress = progressText(entry.DoneSubPlans, entry.TotalSubPlans)
	}
	return OverviewHeader{
		ExternalID:   wi.ExternalID,
		Title:        wi.Title,
		StatusLabel:  string(wi.State),
		UpdatedAt:    entry.LastActivity,
		ProgressText: progress,
		Badges:       badges,
	}
}

// buildPlanView returns the plan summary card.
func (Model) buildPlanView(wi domain.Session, hasPlan bool, plan domain.Plan, subPlans []domain.TaskPlan) OverviewPlan {
	out := OverviewPlan{StateLabel: planStateLabel(wi.State)}
	if !hasPlan {
		out.ActionText = noPlanActionText(wi.State)
		return out
	}
	out.Exists = true
	out.Version = plan.Version
	out.UpdatedAt = plan.UpdatedAt
	out.RepoCount = len(subPlans)
	out.FAQCount = len(plan.FAQ)
	out.ActionText = planActionText(wi.State)
	return out
}

// buildTaskRows returns one row per sub-plan with the latest/waiting/
// interrupted agent session.
func (Model) buildTaskRows(snapshot logic.InitialSnapshot, wi domain.Session, subPlans []domain.TaskPlan) []OverviewTaskRow {
	if len(subPlans) == 0 {
		return nil
	}
	rows := make([]OverviewTaskRow, 0, len(subPlans))
	for _, sp := range subPlans {
		latest, waiting, interrupted := latestTaskForSubPlan(snapshot, wi.ID, sp.ID)
		row := OverviewTaskRow{
			RepoName:       sp.RepositoryName,
			TaskPlanStatus: humanTaskPlanStatus(sp.Status),
			UpdatedAt:      sp.UpdatedAt,
		}
		if latest != nil {
			row.SessionID = latest.ID
			row.SessionTitle = taskSidebarSessionTitle(latest)
			row.SessionStatus = sessionStatusLabel(latest.Status)
			row.HarnessName = latest.HarnessName
			if latest.UpdatedAt.After(row.UpdatedAt) {
				row.UpdatedAt = latest.UpdatedAt
			}
		}
		switch {
		case waiting != nil:
			row.Note = buildQuestionNote(snapshot, waiting.ID)
		case interrupted != nil:
			row.Note = "Interrupted"
		case latest != nil && latest.Status == domain.AgentSessionFailed:
			row.Note = "Failed"
		case wi.State == domain.SessionReviewing:
			row.Note = buildReviewNote(snapshot, wi.ID, sp.ID)
		case sp.Status == domain.SubPlanCompleted:
			row.Note = "Completed"
		}
		rows = append(rows, row)
	}
	return rows
}

// buildSources returns a normalized list of source items.
func (Model) buildSources(wi domain.Session) []OverviewSourceItem {
	if wi.Source == "manual" {
		return nil
	}
	ref := ""
	if len(wi.SourceItemIDs) == 1 {
		ref = wi.SourceItemIDs[0]
	}
	if ref == "" {
		ref = wi.ExternalID
	}
	return []OverviewSourceItem{{
		Provider: providerLabel(wi.Source),
		Ref:      ref,
		Title:    wi.Title,
		URL:      sessionURL(wi),
	}}
}

// buildExternalLifecycle summarizes the external tracker state. Snapshot
// artifacts for the work item are projected into the external Reviews list
// so the overview surfaces PR/MR rows without an extra RPC.
func (Model) buildExternalLifecycle(wi domain.Session, snapshot logic.InitialSnapshot) OverviewExternalLifecycle {
	out := OverviewExternalLifecycle{}
	for _, ref := range sessionTrackerRefs(wi.Metadata) {
		out.TrackerRefs = append(out.TrackerRefs, formatTrackerRef(ref))
	}
	for _, item := range snapshot.Artifacts[wi.ID] {
		out.Reviews = append(out.Reviews, OverviewReviewRow{
			Kind:     item.Kind,
			RepoName: item.RepoName,
			Ref:      item.Ref,
			URL:      item.URL,
			State:    item.State,
			Branch:   item.Branch,
		})
	}
	return out
}

// buildActionCards returns the deterministic set of action cards for a
// work item. The ordering is stable: plan_review, reviewing,
// continuation, failed, finalize, completed, merged, per-session
// question, per-session interrupted, grouped interrupted.
//
// The implementation mirrors (a *App).buildOverviewActions in the TUI but
// is pure: it has no access to App state, so the live-instance /
// resume-in-flight gating the TUI applies is approximated as "no gating"
// here. Daemon-side callers that need a live-instance check should
// extend the snapshot with live owner-instance IDs and post-filter the
func (Model) buildActionCards(snapshot logic.InitialSnapshot, wi domain.Session, hasPlan bool, plan domain.Plan, subPlans []domain.TaskPlan) []OverviewActionCard {
	actions := make([]OverviewActionCard, 0, 4)
	if wi.State == domain.SessionPlanReview && hasPlan {
		affected := make([]string, 0, len(subPlans))
		for _, sp := range subPlans {
			affected = append(affected, sp.RepositoryName)
		}
		context := []string{}
		if plan.Version > 0 {
			context = append(context, "Version: v"+itoa(plan.Version))
		}
		if !plan.UpdatedAt.IsZero() {
			context = append(context, "Updated: "+formatAbsoluteTime(plan.UpdatedAt))
		}
		if len(subPlans) > 0 {
			context = append(context, "Affected repos: "+itoa(len(subPlans)))
		}
		if len(plan.FAQ) > 0 {
			context = append(context, "Open FAQ items: "+itoa(len(plan.FAQ)))
		}
		actions = append(actions, OverviewActionCard{
			Kind:     ActionPlanReview,
			Title:    "Plan review required",
			Blocked:  "Implementation is waiting for plan approval",
			Why:      "The plan must be approved, revised, or rejected before implementation can continue.",
			Affected: affected,
			Context:  context,
		})
	}
	if card := buildReviewActionCard(wi, subPlans, snapshot); card != nil {
		actions = append(actions, *card)
	}
	if card := buildContinuationRecoveryActionCard(wi, subPlans, snapshot); card != nil {
		actions = append(actions, *card)
	}
	if card := buildFailedActionCard(wi, subPlans, snapshot); card != nil {
		actions = append(actions, *card)
	}
	if card := buildFinalizeActionCard(wi, subPlans, snapshot); card != nil {
		actions = append(actions, *card)
	}
	if card := buildCompletedActionCard(wi, subPlans); card != nil {
		actions = append(actions, *card)
	}
	if wi.State == domain.SessionMerged {
		actions = append(actions, OverviewActionCard{
			Kind:    ActionMerged,
			Title:   "All PRs merged",
			Blocked: "This work item has been merged and is now complete.",
			Why:     "All linked pull requests have been merged. No further action is needed.",
		})
	}

	// Per-leaf question and planning-interrupted cards.
	wiSessions := sessionsForWorkItem(snapshot, wi.ID)
	leafSet := make(map[string]bool, len(wiSessions))
	for _, leaf := range domain.LeafAgentSessions(wiSessions) {
		leafSet[leaf.ID] = true
	}
	resumable := domain.ResumableAgentSessionLeaves(wiSessions)
	planningInterrupted := make(map[string]domain.AgentSession)
	var interruptedSessions []domain.AgentSession
	for _, leaf := range resumable {
		if leaf.Kind == domain.AgentSessionKindPlanning {
			planningInterrupted[leaf.ID] = leaf
			continue
		}
		interruptedSessions = append(interruptedSessions, leaf)
	}
	for _, s := range wiSessions {
		if !leafSet[s.ID] {
			continue
		}
		if s.Status == domain.AgentSessionWaitingForAnswer {
			for _, q := range snapshot.Questions[s.ID] {
				if !isOpenQuestion(q) {
					continue
				}
				actions = append(actions, OverviewActionCard{
					Kind:      ActionQuestion,
					Title:     questionTitle(s),
					Blocked:   summarizeText(q.Content, 120),
					Why:       questionWhy(s),
					Affected:  []string{agentSessionAffected(s)},
					Context:   questionContext(s, q),
					CanAct:    true,
					SessionID: s.ID,
				})
				break
			}
		}
		if s.Status == domain.AgentSessionInterrupted {
			if planning, ok := planningInterrupted[s.ID]; ok {
				actions = append(actions, OverviewActionCard{
					Kind:    ActionInterrupted,
					Title:   "Planning was interrupted",
					Blocked: "Planning",
					Why:     "The planning harness was explicitly stopped. Resume will restart planning from the beginning.",
					Context: []string{
						"Last update: " + formatAbsoluteTime(planning.UpdatedAt),
						"Cause: planning harness was explicitly stopped",
					},
					CanAct:    true,
					SessionID: planning.ID,
				})
			}
		}
	}

	// Grouped interrupted-resume card for non-planning leaves. Mirrors the
	// TUI's active-work-item filter: leaves whose work item is currently in
	// planning or implementing state are managed by the active pipeline and
	// must not be surfaced as user-resumable from this card.
	activeWorkItemIDs := activeWorkItemIDSet(snapshot)
	filteredInterruptedSessions := make([]domain.AgentSession, 0, len(interruptedSessions))
	for _, session := range interruptedSessions {
		if activeWorkItemIDs[session.WorkItemID] {
			continue
		}
		filteredInterruptedSessions = append(filteredInterruptedSessions, session)
	}
	if len(filteredInterruptedSessions) > 0 {
		actions = append(actions, buildInterruptedTasksActionCard(filteredInterruptedSessions))
	}
	return actions
}

// activeWorkItemIDSet returns the set of work-item IDs that are currently
// in planning or implementing state. Sessions belonging to these work
// items are managed by the active pipeline and must be excluded from
// user-resumable interrupted cards, mirroring the TUI filter.
func activeWorkItemIDSet(snapshot logic.InitialSnapshot) map[string]bool {
	out := make(map[string]bool)
	for _, wi := range snapshot.Sessions {
		if wi.State == domain.SessionPlanning || wi.State == domain.SessionImplementing {
			out[wi.ID] = true
		}
	}
	return out
}

// buildActivity returns up to three most-recent activity summary items.
func (Model) buildActivity(wi domain.Session, hasPlan bool, plan domain.Plan, snapshot logic.InitialSnapshot) []OverviewActivityItem {
	items := make([]OverviewActivityItem, 0, 8)
	if hasPlan && !plan.UpdatedAt.IsZero() {
		items = append(items, OverviewActivityItem{
			Summary:   "Plan v" + itoa(plan.Version) + " updated",
			Timestamp: plan.UpdatedAt,
		})
	}
	for _, s := range sessionsForWorkItem(snapshot, wi.ID) {
		var summary string
		switch s.Status {
		case domain.AgentSessionWaitingForAnswer:
			if hasOpenQuestionForSession(snapshot, s.ID) {
				summary = sessionDisplayName(s) + " asked a question"
			}
		case domain.AgentSessionInterrupted:
			summary = sessionDisplayName(s) + " interrupted"
		case domain.AgentSessionFailed:
			summary = sessionDisplayName(s) + " failed"
		case domain.AgentSessionCompleted:
			summary = sessionDisplayName(s) + " completed"
		}
		if summary != "" {
			items = append(items, OverviewActivityItem{Summary: summary, Timestamp: s.UpdatedAt})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Timestamp.After(items[j].Timestamp) })
	if len(items) > 3 {
		items = items[:3]
	}
	return items
}

// aggregateArtifactStates returns the worst-case review and CI aggregate
// state across the work item's daemon-owned artifact read model. The priority
// matches the legacy TUI helpers: changes_requested > approved > none, and
// failure > in_progress > success > none.
func (Model) aggregateArtifactStates(snapshot logic.InitialSnapshot, workItemID string) (string, string, bool) {
	items := snapshot.Artifacts[workItemID]
	if len(items) == 0 {
		return "", "", false
	}
	return aggregateArtifactReviewState(items), aggregateArtifactCIState(items), true
}

func aggregateArtifactReviewState(items []logic.ArtifactItem) string {
	hasApproved := false
	for _, item := range items {
		for _, review := range item.Reviews {
			switch review.State {
			case "changes_requested":
				return "changes_requested"
			case "approved":
				hasApproved = true
			}
		}
	}
	if hasApproved {
		return "approved"
	}
	return ""
}

func aggregateArtifactCIState(items []logic.ArtifactItem) string {
	hasChecks := false
	for _, item := range items {
		for _, check := range item.Checks {
			hasChecks = true
			if check.Conclusion != "" && check.Conclusion != "success" && check.Conclusion != "neutral" && check.Conclusion != "skipped" {
				return "failure"
			}
		}
	}
	if !hasChecks {
		return ""
	}
	for _, item := range items {
		for _, check := range item.Checks {
			if check.Status == "in_progress" || check.Status == "queued" {
				return "in_progress"
			}
		}
	}
	return "success"
}

// ---- Helpers shared across the read model ------------------------------

func findSession(sessions []domain.Session, id string) (domain.Session, bool) {
	for _, s := range sessions {
		if s.ID == id {
			return s, true
		}
	}
	return domain.Session{}, false
}

func sessionsForWorkItem(snapshot logic.InitialSnapshot, workItemID string) []domain.AgentSession {
	out := make([]domain.AgentSession, 0, len(snapshot.AgentSessions))
	for _, s := range snapshot.AgentSessions {
		if s.WorkItemID == workItemID {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out
}

func graphLeavesForWorkItem(snapshot logic.InitialSnapshot, workItemID string) []domain.AgentSession {
	return domain.LeafAgentSessions(sessionsForWorkItem(snapshot, workItemID))
}

func hasOpenQuestionForSession(snapshot logic.InitialSnapshot, sessionID string) bool {
	for _, q := range snapshot.Questions[sessionID] {
		if isOpenQuestion(q) {
			return true
		}
	}
	return false
}

func isOpenQuestion(q domain.Question) bool {
	return q.Status == domain.QuestionPending || q.Status == domain.QuestionEscalated
}

func taskSidebarEntryID(workItemID, suffix string) string {
	return workItemID + ":" + suffix
}

func buildTaskSessionEntry(s domain.AgentSession, wi domain.Session, defaultRepo string) SidebarEntry {
	repo := s.RepositoryName
	if repo == "" {
		repo = defaultRepo
	}
	if repo == "" {
		repo = "Repository"
	}
	return SidebarEntry{
		Kind:           SidebarTaskSession,
		ID:             s.ID,
		WorkItemID:     wi.ID,
		SessionID:      s.ID,
		Title:          taskSidebarSessionTitle(&s),
		State:          wi.State,
		SessionStatus:  s.Status,
		RepositoryName: repo,
		LastActivity:   s.UpdatedAt,
		CreatedAt:      s.CreatedAt,
	}
}

func groupAgentSessions(sessions []domain.AgentSession) (planning []domain.AgentSession, foreman *domain.AgentSession, repos map[string][]domain.AgentSession) {
	planning = make([]domain.AgentSession, 0)
	repos = make(map[string][]domain.AgentSession)
	var foremanSessions []domain.AgentSession
	for _, s := range sessions {
		switch s.Kind {
		case domain.AgentSessionKindPlanning:
			planning = append(planning, s)
		case domain.AgentSessionKindForeman:
			foremanSessions = append(foremanSessions, s)
		case domain.AgentSessionKindImplementation, domain.AgentSessionKindReview, domain.AgentSessionKindManual:
			name := s.RepositoryName
			if name == "" {
				if s.Kind == domain.AgentSessionKindReview {
					name = "Review"
				} else if s.Kind == domain.AgentSessionKindManual {
					name = "Manual"
				} else {
					name = "Repository"
				}
			}
			repos[name] = append(repos[name], s)
		}
	}
	sort.SliceStable(planning, func(i, j int) bool {
		return planning[i].CreatedAt.Before(planning[j].CreatedAt)
	})
	if len(foremanSessions) > 0 {
		sort.SliceStable(foremanSessions, func(i, j int) bool {
			return foremanSessions[i].UpdatedAt.After(foremanSessions[j].UpdatedAt)
		})
		foreman = &foremanSessions[0]
	}
	for name := range repos {
		sort.SliceStable(repos[name], func(i, j int) bool {
			return repos[name][i].CreatedAt.Before(repos[name][j].CreatedAt)
		})
	}
	return planning, foreman, repos
}

func sortedRepoNames(repos map[string][]domain.AgentSession) []string {
	names := make([]string, 0, len(repos))
	for name := range repos {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func latestTaskForSubPlan(snapshot logic.InitialSnapshot, workItemID, subPlanID string) (latest, waiting, interrupted *domain.AgentSession) {
	tasks := make([]domain.AgentSession, 0)
	for _, s := range sessionsForWorkItem(snapshot, workItemID) {
		if s.SubPlanID == subPlanID {
			tasks = append(tasks, s)
		}
	}
	if len(tasks) == 0 {
		return nil, nil, nil
	}
	sort.SliceStable(tasks, func(i, j int) bool {
		if !tasks[i].UpdatedAt.Equal(tasks[j].UpdatedAt) {
			return tasks[i].UpdatedAt.After(tasks[j].UpdatedAt)
		}
		return tasks[i].CreatedAt.After(tasks[j].CreatedAt)
	})
	leafSet := make(map[string]bool, len(tasks))
	for _, leaf := range domain.LeafAgentSessions(tasks) {
		leafSet[leaf.ID] = true
	}
	for i := range tasks {
		task := tasks[i]
		if latest == nil {
			t := task
			latest = &t
		}
		if !leafSet[task.ID] {
			continue
		}
		if waiting == nil && task.Status == domain.AgentSessionWaitingForAnswer && hasOpenQuestionForSession(snapshot, task.ID) {
			t := task
			waiting = &t
		}
		if interrupted == nil && task.Status == domain.AgentSessionInterrupted {
			t := task
			interrupted = &t
		}
	}
	return latest, waiting, interrupted
}

func latestImplementationSession(snapshot logic.InitialSnapshot, workItemID, subPlanID string) *domain.AgentSession {
	var latest *domain.AgentSession
	for _, s := range sessionsForWorkItem(snapshot, workItemID) {
		if s.Kind != domain.AgentSessionKindImplementation || s.Status != domain.AgentSessionCompleted {
			continue
		}
		if s.SubPlanID != subPlanID {
			continue
		}
		t := s
		if latest == nil || t.UpdatedAt.After(latest.UpdatedAt) {
			latest = &t
		}
	}
	return latest
}

func buildReviewActionCard(wi domain.Session, subPlans []domain.TaskPlan, snapshot logic.InitialSnapshot) *OverviewActionCard {
	if wi.State != domain.SessionReviewing {
		return nil
	}
	reviewRepos := reviewResultsForOverview(snapshot, wi.ID, subPlans)
	affected := make([]string, 0, len(reviewRepos))
	critiqueCount := 0
	var firstCritique string
	for _, repo := range reviewRepos {
		if len(repo.Critiques) == 0 {
			continue
		}
		affected = append(affected, repo.RepoName)
		critiqueCount += len(repo.Critiques)
		if firstCritique == "" {
			firstCritique = summarizeText(repo.Critiques[0].Description, 160)
		}
	}
	escalatedRepos := make([]string, 0, len(subPlans))
	for _, sp := range subPlans {
		if sp.Status == domain.SubPlanEscalated {
			escalatedRepos = append(escalatedRepos, sp.RepositoryName)
			if !contains(affected, sp.RepositoryName) {
				affected = append(affected, sp.RepositoryName)
			}
		}
	}
	if critiqueCount == 0 && len(escalatedRepos) == 0 {
		return nil
	}
	context := []string{
		"Affected repos: " + itoa(len(affected)),
		"Open critiques: " + itoa(critiqueCount),
	}
	if len(escalatedRepos) > 0 {
		context = append(context, "Escalated repos: "+itoa(len(escalatedRepos)))
	}
	if firstCritique != "" {
		context = append(context, "First critique: "+firstCritique)
	}
	return &OverviewActionCard{
		Kind:     ActionReviewing,
		Title:    "Review requires decision",
		Blocked:  "Critiques are waiting for a human decision",
		Why:      "You can extend review with another implementation pass, override accept, or fail the session.",
		Affected: affected,
		Context:  context,
	}
}

func buildContinuationRecoveryActionCard(wi domain.Session, subPlans []domain.TaskPlan, snapshot logic.InitialSnapshot) *OverviewActionCard {
	if wi.State != domain.SessionImplementing || len(subPlans) == 0 {
		return nil
	}
	incompleteSubPlans := make(map[string]string)
	for _, sp := range subPlans {
		if sp.Status == domain.SubPlanPending || sp.Status == domain.SubPlanInProgress {
			incompleteSubPlans[sp.ID] = sp.RepositoryName
		}
	}
	if len(incompleteSubPlans) == 0 {
		return nil
	}
	for _, s := range sessionsForWorkItem(snapshot, wi.ID) {
		if isOverviewActiveAgentSession(s.Status) {
			return nil
		}
	}
	latestImpl := latestCompletedImplementationForIncomplete(snapshot, wi.ID, incompleteSubPlans)
	if latestImpl == nil {
		return nil
	}
	affected := make([]string, 0, len(incompleteSubPlans))
	seen := make(map[string]bool, len(incompleteSubPlans))
	// Iterate the sub-plans slice (stable order) instead of the
	// incompleteSubPlans map so the affected list is deterministic across
	// runs even when several sub-plans share a repository name.
	for _, sp := range subPlans {
		if sp.Status != domain.SubPlanPending && sp.Status != domain.SubPlanInProgress {
			continue
		}
		if sp.RepositoryName == "" || seen[sp.RepositoryName] {
			continue
		}
		seen[sp.RepositoryName] = true
		affected = append(affected, sp.RepositoryName)
	}
	return &OverviewActionCard{
		Kind:     ActionInterrupted,
		Title:    "Continuation needs recovery",
		Blocked:  "Implementation finished, but review/finalization did not complete",
		Why:      "Resume will continue the saved post-implementation work without waiting for startup to run agents automatically.",
		Affected: affected,
		Context: []string{
			"Last implementation: " + formatAbsoluteTime(latestImpl.UpdatedAt),
			"Recovery starts only after you press Resume.",
		},
		CanAct:    true,
		SessionID: latestImpl.ID,
	}
}

func buildFailedActionCard(wi domain.Session, subPlans []domain.TaskPlan, snapshot logic.InitialSnapshot) *OverviewActionCard {
	if wi.State != domain.SessionFailed {
		return nil
	}
	affected := make([]string, 0)
	seen := make(map[string]bool)
	addAffected := func(repo string) {
		if repo == "" {
			repo = "(unknown)"
		}
		if seen[repo] {
			return
		}
		seen[repo] = true
		affected = append(affected, repo)
	}
	for _, sp := range subPlans {
		if sp.Status == domain.SubPlanFailed {
			addAffected(sp.RepositoryName)
		}
	}
	subPlanRepos := make(map[string]string, len(subPlans))
	for _, sp := range subPlans {
		subPlanRepos[sp.ID] = sp.RepositoryName
	}
	for _, leaf := range domain.RetryableAgentSessionLeaves(sessionsForWorkItem(snapshot, wi.ID)) {
		addAffected(firstNonEmpty(leaf.RepositoryName, subPlanRepos[leaf.SubPlanID], sessionDisplayName(leaf)))
	}
	for _, leaf := range domain.ResumableAgentSessionLeaves(sessionsForWorkItem(snapshot, wi.ID)) {
		addAffected(firstNonEmpty(leaf.RepositoryName, subPlanRepos[leaf.SubPlanID], sessionDisplayName(leaf)))
	}
	if len(affected) == 0 {
		affected = []string{"(unknown)"}
	}
	reviewRepos := reviewResultsForOverview(snapshot, wi.ID, subPlans)
	critiqueCount := 0
	for _, r := range reviewRepos {
		critiqueCount += len(r.Critiques)
	}
	context := []string{"Failed or interrupted repos: " + itoa(len(affected)) + " of " + itoa(len(subPlans))}
	if critiqueCount > 0 {
		context = append(context, "Outstanding critiques: "+itoa(critiqueCount))
	}
	return &OverviewActionCard{
		Kind:     ActionFailed,
		Title:    "Implementation failed",
		Blocked:  itoa(len(affected)) + " repo(s) failed or were interrupted during implementation or review",
		Why:      "You can retry the failed or interrupted repos or inspect their session logs for details.",
		Affected: affected,
		Context:  context,
	}
}

func buildFinalizeActionCard(wi domain.Session, subPlans []domain.TaskPlan, snapshot logic.InitialSnapshot) *OverviewActionCard {
	if wi.State != domain.SessionImplementing || len(subPlans) == 0 {
		return nil
	}
	affected := make([]string, 0, len(subPlans))
	for _, sp := range subPlans {
		if sp.Status != domain.SubPlanCompleted {
			return nil
		}
		affected = append(affected, sp.RepositoryName)
	}
	for _, s := range sessionsForWorkItem(snapshot, wi.ID) {
		if isOverviewActiveAgentSession(s.Status) {
			return nil
		}
	}
	return &OverviewActionCard{
		Kind:     ActionFinalize,
		Title:    "Finalization needed",
		Blocked:  "Repo work finished, but the work item is still marked implementing",
		Why:      "Finalize retries the commit/push/completion step without rerunning implementation agents.",
		Affected: affected,
		Context: []string{
			"Completed repos: " + itoa(len(affected)) + " of " + itoa(len(subPlans)),
			"Use this after verifying the worktree and remote branch are correct.",
		},
	}
}

func buildCompletedActionCard(wi domain.Session, subPlans []domain.TaskPlan) *OverviewActionCard {
	if wi.State != domain.SessionCompleted {
		return nil
	}
	affected := make([]string, 0, len(subPlans))
	for _, sp := range subPlans {
		if sp.Status == domain.SubPlanCompleted {
			affected = append(affected, sp.RepositoryName)
		}
	}
	return &OverviewActionCard{
		Kind:     ActionCompleted,
		Title:    "Implementation completed",
		Why:      "The implementation is done. You can revise the plan or inspect the results.",
		Affected: affected,
		Context:  []string{"Completed repos: " + itoa(len(affected)) + " of " + itoa(len(subPlans))},
	}
}

func buildInterruptedTasksActionCard(sessions []domain.AgentSession) OverviewActionCard {
	copied := append([]domain.AgentSession(nil), sessions...)
	sort.SliceStable(copied, func(i, j int) bool {
		left := firstNonEmpty(copied[i].RepositoryName, sessionDisplayName(copied[i]))
		right := firstNonEmpty(copied[j].RepositoryName, sessionDisplayName(copied[j]))
		if left == right {
			return copied[i].ID < copied[j].ID
		}
		return left < right
	})
	affected := make([]string, 0, len(copied))
	latest := copied[0].UpdatedAt
	for _, s := range copied {
		display := firstNonEmpty(s.RepositoryName, sessionDisplayName(s))
		affected = append(affected, display)
		if s.UpdatedAt.After(latest) {
			latest = s.UpdatedAt
		}
	}
	context := []string{
		"Last update: " + formatAbsoluteTime(latest),
		"Interrupted tasks: " + itoa(len(copied)),
		"Cause: previous substrate owner stopped heartbeating while the agents were running",
	}
	title := "Interrupted tasks need recovery"
	blocked := "Interrupted tasks need recovery"
	why := "These tasks were interrupted and cannot continue until they are resumed."
	if len(copied) == 1 {
		title = "Interrupted task needs recovery"
		blocked = firstNonEmpty(copied[0].RepositoryName, sessionDisplayName(copied[0]))
		why = "This task was interrupted and cannot continue until it is resumed or abandoned."
	}
	return OverviewActionCard{
		Kind:      ActionInterrupted,
		Title:     title,
		Blocked:   blocked,
		Why:       why,
		Affected:  affected,
		Context:   context,
		CanAct:    true,
		SessionID: copied[0].ID,
	}
}

// RepoReviewResult mirrors the TUI's RepoReviewResult type. It is exported
// so the TUI can consume it directly.
type RepoReviewResult struct {
	RepoName  string
	Cycles    []domain.ReviewCycle
	Critiques []domain.Critique
}

func reviewResultsForOverview(snapshot logic.InitialSnapshot, workItemID string, subPlans []domain.TaskPlan) []RepoReviewResult {
	results := make([]RepoReviewResult, 0, len(subPlans))
	for _, sp := range subPlans {
		impl := latestImplementationSession(snapshot, workItemID, sp.ID)
		if impl == nil {
			continue
		}
		cycles := snapshot.Reviews[impl.ID]
		latest, critiques := latestReviewCycle(cycles, snapshot.Critiques)
		rr := RepoReviewResult{RepoName: sp.RepositoryName, Critiques: critiques}
		if latest != nil {
			rr.Cycles = []domain.ReviewCycle{*latest}
		}
		results = append(results, rr)
	}
	return results
}

func latestReviewCycle(cycles []domain.ReviewCycle, critiquesByCycle map[string][]domain.Critique) (*domain.ReviewCycle, []domain.Critique) {
	if len(cycles) == 0 {
		return nil, nil
	}
	latest := cycles[0]
	for _, c := range cycles[1:] {
		if c.CycleNumber > latest.CycleNumber || (c.CycleNumber == latest.CycleNumber && c.UpdatedAt.After(latest.UpdatedAt)) {
			latest = c
		}
	}
	var critiques []domain.Critique
	if critiquesByCycle != nil {
		critiques = critiquesByCycle[latest.ID]
	}
	return &latest, critiques
}

func isOverviewActiveAgentSession(status domain.AgentSessionStatus) bool {
	switch status {
	case domain.AgentSessionPending, domain.AgentSessionRunning, domain.AgentSessionWaitingForAnswer:
		return true
	}
	return false
}

// ---- Label / formatting helpers (kept here to avoid leaking the TUI
// view package) -------------------------------------------------------

func planStateLabel(state domain.SessionState) string {
	switch state {
	case domain.SessionIngested:
		return "No plan yet"
	case domain.SessionPlanning:
		return "Plan in progress"
	case domain.SessionPlanReview:
		return "Plan review needed"
	case domain.SessionApproved:
		return "Approved"
	case domain.SessionImplementing:
		return "Approved plan"
	case domain.SessionReviewing:
		return "Final plan"
	case domain.SessionCompleted:
		return "Final plan"
	case domain.SessionFailed:
		return "Last known plan"
	}
	return "Plan"
}

func noPlanActionText(state domain.SessionState) string {
	switch state {
	case domain.SessionIngested:
		return "Press [Enter] to start planning."
	case domain.SessionPlanning:
		return "Planning is in progress. The overview shows a bounded draft snapshot when available."
	case domain.SessionFailed:
		return "No persisted plan is available for this failed session."
	}
	return "No plan is available yet."
}

func planActionText(state domain.SessionState) string {
	switch state {
	case domain.SessionPlanReview:
		return "Review the bounded excerpt here, or press [i] for the full plan with approval controls."
	case domain.SessionApproved, domain.SessionImplementing, domain.SessionReviewing, domain.SessionCompleted:
		return "Press [i] to inspect the full plan in an overlay."
	case domain.SessionFailed:
		return "This is the last known plan snapshot before the failure."
	}
	return ""
}

func humanTaskPlanStatus(status domain.TaskPlanStatus) string {
	switch status {
	case domain.SubPlanPending:
		return "Pending"
	case domain.SubPlanInProgress:
		return "In progress"
	case domain.SubPlanCompleted:
		return "Completed"
	case domain.SubPlanFailed:
		return "Failed"
	case domain.SubPlanEscalated:
		return "Needs review"
	}
	return string(status)
}

func sessionStatusLabel(status domain.AgentSessionStatus) string {
	switch status {
	case domain.AgentSessionPending:
		return "Pending"
	case domain.AgentSessionRunning:
		return "Running"
	case domain.AgentSessionWaitingForAnswer:
		return "Waiting for answer"
	case domain.AgentSessionCompleted:
		return "Completed"
	case domain.AgentSessionInterrupted:
		return "Interrupted"
	case domain.AgentSessionFailed:
		return "Failed"
	}
	return string(status)
}

func taskSidebarSessionTitle(s *domain.AgentSession) string {
	switch s.Kind {
	case domain.AgentSessionKindPlanning:
		return "Session " + shortID(s.ID)
	case domain.AgentSessionKindReview:
		return "Review " + shortID(s.ID)
	case domain.AgentSessionKindManual:
		return "Manual " + shortID(s.ID)
	}
	return "Implementation " + shortID(s.ID)
}

func sessionDisplayName(s domain.AgentSession) string {
	switch s.Kind {
	case domain.AgentSessionKindPlanning:
		return "Planning"
	case domain.AgentSessionKindReview:
		return firstNonEmpty(s.RepositoryName, "Review")
	case domain.AgentSessionKindManual:
		return firstNonEmpty(s.RepositoryName, "Manual")
	}
	return firstNonEmpty(s.RepositoryName, "Task")
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func buildQuestionNote(snapshot logic.InitialSnapshot, sessionID string) string {
	for _, q := range snapshot.Questions[sessionID] {
		if isOpenQuestion(q) {
			return "Waiting for answer: " + summarizeText(q.Content, 72)
		}
	}
	return "Waiting for answer"
}

func buildReviewNote(snapshot logic.InitialSnapshot, workItemID, subPlanID string) string {
	impl := latestImplementationSession(snapshot, workItemID, subPlanID)
	if impl == nil {
		return "Under review"
	}
	cycles := snapshot.Reviews[impl.ID]
	latest, critiques := latestReviewCycle(cycles, snapshot.Critiques)
	if latest == nil {
		return "Under review"
	}
	switch latest.Status {
	case domain.ReviewCyclePassed:
		return "Review passed"
	case domain.ReviewCycleFailed:
		return "Review failed"
	case domain.ReviewCycleCritiquesFound, domain.ReviewCycleReimplementing:
		if len(critiques) == 0 {
			return "Under review"
		}
		return itoa(len(critiques)) + " critique(s)"
	default:
		return "Under review"
	}
}

func questionTitle(s domain.AgentSession) string {
	if s.Kind == domain.AgentSessionKindPlanning {
		return "Planning question"
	}
	return "Question waiting for answer"
}

func questionWhy(s domain.AgentSession) string {
	if s.Kind == domain.AgentSessionKindPlanning {
		return "The planner is paused until you answer this question."
	}
	return "This repo task is paused until a human answers the escalated question."
}

func questionContext(s domain.AgentSession, q domain.Question) []string {
	ctx := []string{
		"Asked: " + formatAbsoluteTime(q.CreatedAt),
		summarizeText(q.Content, 160),
	}
	if q.Context != "" {
		ctx = append(ctx, summarizeText(q.Context, 160))
	}
	return ctx
}

func agentSessionAffected(s domain.AgentSession) string {
	name := firstNonEmpty(s.RepositoryName, sessionDisplayName(s))
	return name + " (" + taskSidebarSessionTitle(&s) + ")"
}

// sessionURL returns a best-effort canonical URL for a work item.
func sessionURL(wi domain.Session) string {
	// The full URL composition lives in the TUI; the read model returns
	// the empty string and lets clients enrich it with their own snapshot
	// of provider data when they need to render links.
	_ = wi
	return ""
}

func sessionTrackerRefs(metadata map[string]any) []domain.TrackerReference {
	// We intentionally do not reach into the metadata map. The TUI's
	// helper decodes provider-specific keys; the read model treats the
	// tracker refs as an extension point and returns no entries until a
	// snapshot provides them in a stable, transport-friendly form.
	_ = metadata
	return nil
}

func formatTrackerRef(_ domain.TrackerReference) string { return "" }

func gitlabExternalLabel(_ domain.Session) string { return "" }

func providerLabel(provider string) string {
	switch provider {
	case "gitlab":
		return "GitLab"
	case "github":
		return "GitHub"
	case "manual":
		return "Manual"
	}
	return provider
}

func progressText(done, total int) string {
	return itoa(done) + "/" + itoa(total) + " repos complete"
}

func formatAbsoluteTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.UTC().Format(time.RFC3339)
}

func summarizeText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func latestCompletedImplementationForIncomplete(snapshot logic.InitialSnapshot, workItemID string, incomplete map[string]string) *domain.AgentSession {
	var latest *domain.AgentSession
	for _, s := range sessionsForWorkItem(snapshot, workItemID) {
		if s.Kind != domain.AgentSessionKindImplementation || s.Status != domain.AgentSessionCompleted {
			continue
		}
		if _, ok := incomplete[s.SubPlanID]; !ok {
			continue
		}
		t := s
		if latest == nil || t.UpdatedAt.After(latest.UpdatedAt) {
			latest = &t
		}
	}
	return latest
}
