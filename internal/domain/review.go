package domain

import "time"

// ReviewCycle is one review pass over an agent session's output.
type ReviewCycle struct {
	ID              string
	CycleNumber     int
	AgentSessionID  string
	ReviewerHarness string
	Summary         string
	Status          ReviewCycleStatus
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// ReviewCycleStatus represents the lifecycle state of a review cycle.
type ReviewCycleStatus string

const (
	ReviewCycleReviewing      ReviewCycleStatus = "reviewing"
	ReviewCycleCritiquesFound ReviewCycleStatus = "critiques_found"
	ReviewCycleReimplementing ReviewCycleStatus = "reimplementing"
	ReviewCyclePassed         ReviewCycleStatus = "passed"
	ReviewCycleFailed         ReviewCycleStatus = "failed"
)

// Critique is a single review finding within a review cycle.
type Critique struct {
	ID            string
	ReviewCycleID string
	FilePath      string
	LineNumber    *int
	Description   string
	Severity      CritiqueSeverity
	Status        CritiqueStatus
	CreatedAt     time.Time
}

// CritiqueSeverity represents the severity of a critique.
type CritiqueSeverity string

const (
	CritiqueCritical CritiqueSeverity = "critical"
	CritiqueMajor    CritiqueSeverity = "major"
	CritiqueMinor    CritiqueSeverity = "minor"
	CritiqueNit      CritiqueSeverity = "nit"
)

// CritiqueStatus represents whether a critique is open or resolved.
type CritiqueStatus string

const (
	CritiqueOpen     CritiqueStatus = "open"
	CritiqueResolved CritiqueStatus = "resolved"
)
