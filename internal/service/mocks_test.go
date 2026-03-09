package service

import (
	"context"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

// MockWorkItemRepository implements repository.WorkItemRepository for testing.
type MockWorkItemRepository struct {
	items map[string]domain.WorkItem
	err   error
}

func NewMockWorkItemRepository() *MockWorkItemRepository {
	return &MockWorkItemRepository{
		items: make(map[string]domain.WorkItem),
	}
}

func (m *MockWorkItemRepository) Get(ctx context.Context, id string) (domain.WorkItem, error) {
	if m.err != nil {
		return domain.WorkItem{}, m.err
	}
	item, ok := m.items[id]
	if !ok {
		return domain.WorkItem{}, repository.ErrNotFound
	}
	return item, nil
}

func (m *MockWorkItemRepository) List(ctx context.Context, filter repository.WorkItemFilter) ([]domain.WorkItem, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []domain.WorkItem
	for _, item := range m.items {
		if filter.WorkspaceID != nil && item.WorkspaceID != *filter.WorkspaceID {
			continue
		}
		if filter.ExternalID != nil && item.ExternalID != *filter.ExternalID {
			continue
		}
		if filter.State != nil && item.State != *filter.State {
			continue
		}
		if filter.Source != nil && item.Source != *filter.Source {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

func (m *MockWorkItemRepository) Create(ctx context.Context, item domain.WorkItem) error {
	if m.err != nil {
		return m.err
	}
	m.items[item.ID] = item
	return nil
}

func (m *MockWorkItemRepository) Update(ctx context.Context, item domain.WorkItem) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.items[item.ID]; !ok {
		return repository.ErrNotFound
	}
	m.items[item.ID] = item
	return nil
}

func (m *MockWorkItemRepository) Delete(ctx context.Context, id string) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.items[id]; !ok {
		return repository.ErrNotFound
	}
	delete(m.items, id)
	return nil
}

// MockPlanRepository implements repository.PlanRepository for testing.
type MockPlanRepository struct {
	plans      map[string]domain.Plan
	byWorkItem map[string]string // workItemID -> planID
	err        error
}

func NewMockPlanRepository() *MockPlanRepository {
	return &MockPlanRepository{
		plans:      make(map[string]domain.Plan),
		byWorkItem: make(map[string]string),
	}
}

func (m *MockPlanRepository) Get(ctx context.Context, id string) (domain.Plan, error) {
	if m.err != nil {
		return domain.Plan{}, m.err
	}
	plan, ok := m.plans[id]
	if !ok {
		return domain.Plan{}, repository.ErrNotFound
	}
	return plan, nil
}

func (m *MockPlanRepository) GetByWorkItemID(ctx context.Context, workItemID string) (domain.Plan, error) {
	if m.err != nil {
		return domain.Plan{}, m.err
	}
	planID, ok := m.byWorkItem[workItemID]
	if !ok {
		return domain.Plan{}, repository.ErrNotFound
	}
	return m.plans[planID], nil
}

func (m *MockPlanRepository) Create(ctx context.Context, plan domain.Plan) error {
	if m.err != nil {
		return m.err
	}
	m.plans[plan.ID] = plan
	m.byWorkItem[plan.WorkItemID] = plan.ID
	return nil
}

func (m *MockPlanRepository) Update(ctx context.Context, plan domain.Plan) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.plans[plan.ID]; !ok {
		return repository.ErrNotFound
	}
	m.plans[plan.ID] = plan
	return nil
}

func (m *MockPlanRepository) Delete(ctx context.Context, id string) error {
	if m.err != nil {
		return m.err
	}
	plan, ok := m.plans[id]
	if !ok {
		return repository.ErrNotFound
	}
	delete(m.byWorkItem, plan.WorkItemID)
	delete(m.plans, id)
	return nil
}

func (m *MockPlanRepository) AppendFAQ(ctx context.Context, entry domain.FAQEntry) error {
	if m.err != nil {
		return m.err
	}
	return nil
}

// MockSubPlanRepository implements repository.SubPlanRepository for testing.
type MockSubPlanRepository struct {
	subPlans map[string]domain.SubPlan
	byPlan   map[string][]string // planID -> []subPlanID
	err      error
}

func NewMockSubPlanRepository() *MockSubPlanRepository {
	return &MockSubPlanRepository{
		subPlans: make(map[string]domain.SubPlan),
		byPlan:   make(map[string][]string),
	}
}

func (m *MockSubPlanRepository) Get(ctx context.Context, id string) (domain.SubPlan, error) {
	if m.err != nil {
		return domain.SubPlan{}, m.err
	}
	sp, ok := m.subPlans[id]
	if !ok {
		return domain.SubPlan{}, repository.ErrNotFound
	}
	return sp, nil
}

func (m *MockSubPlanRepository) ListByPlanID(ctx context.Context, planID string) ([]domain.SubPlan, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []domain.SubPlan
	for _, id := range m.byPlan[planID] {
		result = append(result, m.subPlans[id])
	}
	return result, nil
}

func (m *MockSubPlanRepository) Create(ctx context.Context, sp domain.SubPlan) error {
	if m.err != nil {
		return m.err
	}
	m.subPlans[sp.ID] = sp
	m.byPlan[sp.PlanID] = append(m.byPlan[sp.PlanID], sp.ID)
	return nil
}

func (m *MockSubPlanRepository) Update(ctx context.Context, sp domain.SubPlan) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.subPlans[sp.ID]; !ok {
		return repository.ErrNotFound
	}
	m.subPlans[sp.ID] = sp
	return nil
}

func (m *MockSubPlanRepository) Delete(ctx context.Context, id string) error {
	if m.err != nil {
		return m.err
	}
	sp, ok := m.subPlans[id]
	if !ok {
		return repository.ErrNotFound
	}
	// Remove from byPlan index
	var newIDs []string
	for _, existingID := range m.byPlan[sp.PlanID] {
		if existingID != id {
			newIDs = append(newIDs, existingID)
		}
	}
	m.byPlan[sp.PlanID] = newIDs
	delete(m.subPlans, id)
	return nil
}

// MockWorkspaceRepository implements repository.WorkspaceRepository for testing.
type MockWorkspaceRepository struct {
	workspaces map[string]domain.Workspace
	err        error
}

func NewMockWorkspaceRepository() *MockWorkspaceRepository {
	return &MockWorkspaceRepository{
		workspaces: make(map[string]domain.Workspace),
	}
}

func (m *MockWorkspaceRepository) Get(ctx context.Context, id string) (domain.Workspace, error) {
	if m.err != nil {
		return domain.Workspace{}, m.err
	}
	ws, ok := m.workspaces[id]
	if !ok {
		return domain.Workspace{}, repository.ErrNotFound
	}
	return ws, nil
}

func (m *MockWorkspaceRepository) Create(ctx context.Context, ws domain.Workspace) error {
	if m.err != nil {
		return m.err
	}
	m.workspaces[ws.ID] = ws
	return nil
}

func (m *MockWorkspaceRepository) Update(ctx context.Context, ws domain.Workspace) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.workspaces[ws.ID]; !ok {
		return repository.ErrNotFound
	}
	m.workspaces[ws.ID] = ws
	return nil
}

func (m *MockWorkspaceRepository) Delete(ctx context.Context, id string) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.workspaces[id]; !ok {
		return repository.ErrNotFound
	}
	delete(m.workspaces, id)
	return nil
}

// MockSessionRepository implements repository.SessionRepository for testing.
type MockSessionRepository struct {
	sessions        map[string]domain.AgentSession
	bySubPlan       map[string][]string
	byWorkspace     map[string][]string
	byOwnerInstance map[string][]string
	err             error
}

func NewMockSessionRepository() *MockSessionRepository {
	return &MockSessionRepository{
		sessions:        make(map[string]domain.AgentSession),
		bySubPlan:       make(map[string][]string),
		byWorkspace:     make(map[string][]string),
		byOwnerInstance: make(map[string][]string),
	}
}

func (m *MockSessionRepository) Get(ctx context.Context, id string) (domain.AgentSession, error) {
	if m.err != nil {
		return domain.AgentSession{}, m.err
	}
	s, ok := m.sessions[id]
	if !ok {
		return domain.AgentSession{}, repository.ErrNotFound
	}
	return s, nil
}

func (m *MockSessionRepository) ListBySubPlanID(ctx context.Context, subPlanID string) ([]domain.AgentSession, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []domain.AgentSession
	for _, id := range m.bySubPlan[subPlanID] {
		result = append(result, m.sessions[id])
	}
	return result, nil
}

func (m *MockSessionRepository) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.AgentSession, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []domain.AgentSession
	for _, id := range m.byWorkspace[workspaceID] {
		result = append(result, m.sessions[id])
	}
	return result, nil
}

func (m *MockSessionRepository) ListByOwnerInstanceID(ctx context.Context, instanceID string) ([]domain.AgentSession, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []domain.AgentSession
	for _, id := range m.byOwnerInstance[instanceID] {
		result = append(result, m.sessions[id])
	}
	return result, nil
}

func (m *MockSessionRepository) SearchHistory(ctx context.Context, filter domain.SessionHistoryFilter) ([]domain.SessionHistoryEntry, error) {
	if m.err != nil {
		return nil, m.err
	}
	return nil, nil
}

func (m *MockSessionRepository) Create(ctx context.Context, s domain.AgentSession) error {
	if m.err != nil {
		return m.err
	}
	m.sessions[s.ID] = s
	m.bySubPlan[s.SubPlanID] = append(m.bySubPlan[s.SubPlanID], s.ID)
	m.byWorkspace[s.WorkspaceID] = append(m.byWorkspace[s.WorkspaceID], s.ID)
	if s.OwnerInstanceID != nil {
		m.byOwnerInstance[*s.OwnerInstanceID] = append(m.byOwnerInstance[*s.OwnerInstanceID], s.ID)
	}
	return nil
}

func (m *MockSessionRepository) Update(ctx context.Context, s domain.AgentSession) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.sessions[s.ID]; !ok {
		return repository.ErrNotFound
	}
	m.sessions[s.ID] = s
	return nil
}

func (m *MockSessionRepository) Delete(ctx context.Context, id string) error {
	if m.err != nil {
		return m.err
	}
	s, ok := m.sessions[id]
	if !ok {
		return repository.ErrNotFound
	}
	delete(m.sessions, id)
	// Remove from indexes
	var newSubPlanIDs []string
	for _, existingID := range m.bySubPlan[s.SubPlanID] {
		if existingID != id {
			newSubPlanIDs = append(newSubPlanIDs, existingID)
		}
	}
	m.bySubPlan[s.SubPlanID] = newSubPlanIDs
	var newWorkspaceIDs []string
	for _, existingID := range m.byWorkspace[s.WorkspaceID] {
		if existingID != id {
			newWorkspaceIDs = append(newWorkspaceIDs, existingID)
		}
	}
	m.byWorkspace[s.WorkspaceID] = newWorkspaceIDs
	return nil
}

// MockReviewRepository implements repository.ReviewRepository for testing.
type MockReviewRepository struct {
	cycles    map[string]domain.ReviewCycle
	critiques map[string]domain.Critique
	bySession map[string][]string // sessionID -> []cycleID
	byCycle   map[string][]string // cycleID -> []critiqueID
	err       error
}

func NewMockReviewRepository() *MockReviewRepository {
	return &MockReviewRepository{
		cycles:    make(map[string]domain.ReviewCycle),
		critiques: make(map[string]domain.Critique),
		bySession: make(map[string][]string),
		byCycle:   make(map[string][]string),
	}
}

func (m *MockReviewRepository) GetCycle(ctx context.Context, id string) (domain.ReviewCycle, error) {
	if m.err != nil {
		return domain.ReviewCycle{}, m.err
	}
	c, ok := m.cycles[id]
	if !ok {
		return domain.ReviewCycle{}, repository.ErrNotFound
	}
	return c, nil
}

func (m *MockReviewRepository) ListCyclesBySessionID(ctx context.Context, sessionID string) ([]domain.ReviewCycle, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []domain.ReviewCycle
	for _, id := range m.bySession[sessionID] {
		result = append(result, m.cycles[id])
	}
	return result, nil
}

func (m *MockReviewRepository) CreateCycle(ctx context.Context, rc domain.ReviewCycle) error {
	if m.err != nil {
		return m.err
	}
	m.cycles[rc.ID] = rc
	m.bySession[rc.AgentSessionID] = append(m.bySession[rc.AgentSessionID], rc.ID)
	return nil
}

func (m *MockReviewRepository) UpdateCycle(ctx context.Context, rc domain.ReviewCycle) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.cycles[rc.ID]; !ok {
		return repository.ErrNotFound
	}
	m.cycles[rc.ID] = rc
	return nil
}

func (m *MockReviewRepository) GetCritique(ctx context.Context, id string) (domain.Critique, error) {
	if m.err != nil {
		return domain.Critique{}, m.err
	}
	c, ok := m.critiques[id]
	if !ok {
		return domain.Critique{}, repository.ErrNotFound
	}
	return c, nil
}

func (m *MockReviewRepository) ListCritiquesByReviewCycleID(ctx context.Context, cycleID string) ([]domain.Critique, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []domain.Critique
	for _, id := range m.byCycle[cycleID] {
		result = append(result, m.critiques[id])
	}
	return result, nil
}

func (m *MockReviewRepository) CreateCritique(ctx context.Context, c domain.Critique) error {
	if m.err != nil {
		return m.err
	}
	m.critiques[c.ID] = c
	m.byCycle[c.ReviewCycleID] = append(m.byCycle[c.ReviewCycleID], c.ID)
	return nil
}

func (m *MockReviewRepository) UpdateCritique(ctx context.Context, c domain.Critique) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.critiques[c.ID]; !ok {
		return repository.ErrNotFound
	}
	m.critiques[c.ID] = c
	return nil
}

// MockQuestionRepository implements repository.QuestionRepository for testing.
type MockQuestionRepository struct {
	questions map[string]domain.Question
	bySession map[string][]string
	err       error
}

func NewMockQuestionRepository() *MockQuestionRepository {
	return &MockQuestionRepository{
		questions: make(map[string]domain.Question),
		bySession: make(map[string][]string),
	}
}

func (m *MockQuestionRepository) Get(ctx context.Context, id string) (domain.Question, error) {
	if m.err != nil {
		return domain.Question{}, m.err
	}
	q, ok := m.questions[id]
	if !ok {
		return domain.Question{}, repository.ErrNotFound
	}
	return q, nil
}

func (m *MockQuestionRepository) ListBySessionID(ctx context.Context, sessionID string) ([]domain.Question, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []domain.Question
	for _, id := range m.bySession[sessionID] {
		result = append(result, m.questions[id])
	}
	return result, nil
}

func (m *MockQuestionRepository) Create(ctx context.Context, q domain.Question) error {
	if m.err != nil {
		return m.err
	}
	m.questions[q.ID] = q
	m.bySession[q.AgentSessionID] = append(m.bySession[q.AgentSessionID], q.ID)
	return nil
}

func (m *MockQuestionRepository) Update(ctx context.Context, q domain.Question) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.questions[q.ID]; !ok {
		return repository.ErrNotFound
	}
	m.questions[q.ID] = q
	return nil
}

func (m *MockQuestionRepository) UpdateProposedAnswer(ctx context.Context, id, proposedAnswer string) error {
	if m.err != nil {
		return m.err
	}
	q, ok := m.questions[id]
	if !ok {
		return repository.ErrNotFound
	}
	if q.Status != domain.QuestionEscalated {
		return nil // no-op, matches conditional SQL behaviour
	}
	q.ProposedAnswer = proposedAnswer
	m.questions[id] = q
	return nil
}

// MockInstanceRepository implements repository.InstanceRepository for testing.
type MockInstanceRepository struct {
	instances   map[string]domain.SubstrateInstance
	byWorkspace map[string][]string
	err         error
}

func NewMockInstanceRepository() *MockInstanceRepository {
	return &MockInstanceRepository{
		instances:   make(map[string]domain.SubstrateInstance),
		byWorkspace: make(map[string][]string),
	}
}

func (m *MockInstanceRepository) Get(ctx context.Context, id string) (domain.SubstrateInstance, error) {
	if m.err != nil {
		return domain.SubstrateInstance{}, m.err
	}
	inst, ok := m.instances[id]
	if !ok {
		return domain.SubstrateInstance{}, repository.ErrNotFound
	}
	return inst, nil
}

func (m *MockInstanceRepository) ListByWorkspaceID(ctx context.Context, workspaceID string) ([]domain.SubstrateInstance, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []domain.SubstrateInstance
	for _, id := range m.byWorkspace[workspaceID] {
		result = append(result, m.instances[id])
	}
	return result, nil
}

func (m *MockInstanceRepository) Create(ctx context.Context, inst domain.SubstrateInstance) error {
	if m.err != nil {
		return m.err
	}
	m.instances[inst.ID] = inst
	m.byWorkspace[inst.WorkspaceID] = append(m.byWorkspace[inst.WorkspaceID], inst.ID)
	return nil
}

func (m *MockInstanceRepository) Update(ctx context.Context, inst domain.SubstrateInstance) error {
	if m.err != nil {
		return m.err
	}
	if _, ok := m.instances[inst.ID]; !ok {
		return repository.ErrNotFound
	}
	m.instances[inst.ID] = inst
	return nil
}

func (m *MockInstanceRepository) Delete(ctx context.Context, id string) error {
	if m.err != nil {
		return m.err
	}
	inst, ok := m.instances[id]
	if !ok {
		return repository.ErrNotFound
	}
	delete(m.instances, id)
	var newIDs []string
	for _, existingID := range m.byWorkspace[inst.WorkspaceID] {
		if existingID != id {
			newIDs = append(newIDs, existingID)
		}
	}
	m.byWorkspace[inst.WorkspaceID] = newIDs
	return nil
}
