package orchestrator

import (
	"sort"

	"github.com/beeemT/substrate/internal/domain"
)

// BuildWaves groups sub-plans by Order into sequential execution waves.
// Sub-plans within a wave run concurrently; waves execute sequentially.
//
// Example: SubPlans with Order values [0, 0, 1] produce 2 waves:
//   - Wave 0: 2 parallel sub-plans
//   - Wave 1: 1 sub-plan (runs after all wave 0 sub-plans complete)
func BuildWaves(subPlans []domain.SubPlan) [][]domain.SubPlan {
	if len(subPlans) == 0 {
		return nil
	}

	// Group sub-plans by order
	groups := make(map[int][]domain.SubPlan)
	for _, sp := range subPlans {
		groups[sp.Order] = append(groups[sp.Order], sp)
	}

	// Extract and sort order keys
	orders := make([]int, 0, len(groups))
	for order := range groups {
		orders = append(orders, order)
	}
	sort.Ints(orders)

	// Build waves in order
	waves := make([][]domain.SubPlan, len(orders))
	for i, order := range orders {
		waves[i] = groups[order]
	}

	return waves
}

// WaveStatus represents the status of a wave during execution.
type WaveStatus string

const (
	WavePending   WaveStatus = "pending"
	WaveRunning   WaveStatus = "running"
	WaveCompleted WaveStatus = "completed"
	WaveFailed    WaveStatus = "failed"
)

// WaveState tracks the execution state of a single wave.
type WaveState struct {
	Index      int
	Status     WaveStatus
	SubPlanIDs []string
	StartedAt  int64 // Unix nano timestamp
	EndedAt    int64 // Unix nano timestamp
}

// SubPlanExecution represents the execution state of a single sub-plan.
type SubPlanExecution struct {
	SubPlanID   string
	Order       int
	WaveIndex   int
	Status      domain.SubPlanStatus
	StartedAt   int64 // Unix nano timestamp
	CompletedAt int64 // Unix nano timestamp
	Error       error
}

// ExecutionState tracks the overall state of plan implementation.
type ExecutionState struct {
	PlanID      string
	CurrentWave int
	WaveStates  []WaveState
	Executions  map[string]*SubPlanExecution // sub-plan ID -> execution
}

// NewExecutionState creates a new execution state for a plan.
func NewExecutionState(planID string, subPlans []domain.SubPlan) *ExecutionState {
	waves := BuildWaves(subPlans)

	waveStates := make([]WaveState, len(waves))
	for i, wave := range waves {
		ids := make([]string, len(wave))
		for j, sp := range wave {
			ids[j] = sp.ID
		}
		waveStates[i] = WaveState{
			Index:      i,
			Status:     WavePending,
			SubPlanIDs: ids,
		}
	}

	executions := make(map[string]*SubPlanExecution)
	for i, wave := range waves {
		for _, sp := range wave {
			executions[sp.ID] = &SubPlanExecution{
				SubPlanID: sp.ID,
				Order:     sp.Order,
				WaveIndex: i,
				Status:    domain.SubPlanPending,
			}
		}
	}

	return &ExecutionState{
		PlanID:      planID,
		CurrentWave: 0,
		WaveStates:  waveStates,
		Executions:  executions,
	}
}

// AllWavesCompleted returns true if all waves have completed successfully.
func (s *ExecutionState) AllWavesCompleted() bool {
	if len(s.WaveStates) == 0 {
		return false
	}
	for _, ws := range s.WaveStates {
		if ws.Status != WaveCompleted {
			return false
		}
	}
	return true
}

// CurrentWaveComplete returns true if the current wave has completed.
func (s *ExecutionState) CurrentWaveComplete() bool {
	if s.CurrentWave >= len(s.WaveStates) {
		return true
	}
	return s.WaveStates[s.CurrentWave].Status == WaveCompleted
}

// HasFailed returns true if any wave or sub-plan has failed.
func (s *ExecutionState) HasFailed() bool {
	for _, ws := range s.WaveStates {
		if ws.Status == WaveFailed {
			return true
		}
	}
	for _, ex := range s.Executions {
		if ex.Status == domain.SubPlanFailed {
			return true
		}
	}
	return false
}

// AdvanceWave moves to the next wave if the current one is complete.
// Returns true if advanced, false if no more waves or current not complete.
func (s *ExecutionState) AdvanceWave() bool {
	if !s.CurrentWaveComplete() {
		return false
	}
	if s.CurrentWave+1 >= len(s.WaveStates) {
		return false
	}
	s.CurrentWave++
	return true
}

// GetWaveSubPlans returns the sub-plan IDs for the given wave index.
func (s *ExecutionState) GetWaveSubPlans(waveIndex int) []string {
	if waveIndex < 0 || waveIndex >= len(s.WaveStates) {
		return nil
	}
	return s.WaveStates[waveIndex].SubPlanIDs
}

// StartWave marks a wave as running.
func (s *ExecutionState) StartWave(waveIndex int, startedAt int64) {
	if waveIndex >= 0 && waveIndex < len(s.WaveStates) {
		s.WaveStates[waveIndex].Status = WaveRunning
		s.WaveStates[waveIndex].StartedAt = startedAt
	}
}

// CompleteWave marks a wave as completed.
func (s *ExecutionState) CompleteWave(waveIndex int, endedAt int64) {
	if waveIndex >= 0 && waveIndex < len(s.WaveStates) {
		s.WaveStates[waveIndex].Status = WaveCompleted
		s.WaveStates[waveIndex].EndedAt = endedAt
	}
}

// FailWave marks a wave as failed.
func (s *ExecutionState) FailWave(waveIndex int, endedAt int64) {
	if waveIndex >= 0 && waveIndex < len(s.WaveStates) {
		s.WaveStates[waveIndex].Status = WaveFailed
		s.WaveStates[waveIndex].EndedAt = endedAt
	}
}

// StartSubPlan marks a sub-plan as in progress.
func (s *ExecutionState) StartSubPlan(subPlanID string, startedAt int64) {
	if ex, ok := s.Executions[subPlanID]; ok {
		ex.Status = domain.SubPlanInProgress
		ex.StartedAt = startedAt
	}
}

// CompleteSubPlan marks a sub-plan as completed.
func (s *ExecutionState) CompleteSubPlan(subPlanID string, completedAt int64) {
	if ex, ok := s.Executions[subPlanID]; ok {
		ex.Status = domain.SubPlanCompleted
		ex.CompletedAt = completedAt
	}
}

// FailSubPlan marks a sub-plan as failed.
func (s *ExecutionState) FailSubPlan(subPlanID string, completedAt int64, err error) {
	if ex, ok := s.Executions[subPlanID]; ok {
		ex.Status = domain.SubPlanFailed
		ex.CompletedAt = completedAt
		ex.Error = err
	}
}
