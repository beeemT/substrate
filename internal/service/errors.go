package service

import (
	"fmt"

	"github.com/beeemT/substrate/internal/domain"
)

// ErrInvalidTransition is returned when a state transition is not allowed.
type ErrInvalidTransition struct {
	From   string
	To     string
	Entity string
}

func (e ErrInvalidTransition) Error() string {
	return fmt.Sprintf("invalid state transition for %s: cannot transition from %q to %q", e.Entity, e.From, e.To)
}

// ErrNotFound is returned when an entity is not found.
type ErrNotFound struct {
	Entity string
	ID     string
}

func (e ErrNotFound) Error() string {
	return fmt.Sprintf("%s not found: %s", e.Entity, e.ID)
}

// ErrAlreadyExists is returned when an entity already exists.
type ErrAlreadyExists struct {
	Entity string
	ID     string
}

func (e ErrAlreadyExists) Error() string {
	return fmt.Sprintf("%s already exists: %s", e.Entity, e.ID)
}

// ErrInvalidInput is returned when input validation fails.
type ErrInvalidInput struct {
	Message string
	Field   string
}

func (e ErrInvalidInput) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("invalid input for field %q: %s", e.Field, e.Message)
	}
	return fmt.Sprintf("invalid input: %s", e.Message)
}

// ErrConstraintViolation is returned when a business constraint is violated.
type ErrConstraintViolation struct {
	Message string
}

func (e ErrConstraintViolation) Error() string {
	return fmt.Sprintf("constraint violation: %s", e.Message)
}

// Helper functions to create typed errors

func newInvalidTransitionError(from, to, entity string) error {
	return ErrInvalidTransition{From: from, To: to, Entity: entity}
}

func newNotFoundError(entity, id string) error {
	return ErrNotFound{Entity: entity, ID: id}
}

func newAlreadyExistsError(entity, id string) error {
	return ErrAlreadyExists{Entity: entity, ID: id}
}

func newInvalidInputError(message, field string) error {
	return ErrInvalidInput{Message: message, Field: field}
}

func newConstraintViolationError(message string) error {
	return ErrConstraintViolation{Message: message}
}

// WorkItem state transition helpers
func workItemStateName(s domain.WorkItemState) string {
	return string(s)
}

func planStatusName(s domain.PlanStatus) string {
	return string(s)
}

func subPlanStatusName(s domain.SubPlanStatus) string {
	return string(s)
}

func workspaceStatusName(s domain.WorkspaceStatus) string {
	return string(s)
}

func sessionStatusName(s domain.AgentSessionStatus) string {
	return string(s)
}

func reviewCycleStatusName(s domain.ReviewCycleStatus) string {
	return string(s)
}

func questionStatusName(s domain.QuestionStatus) string {
	return string(s)
}

func critiqueStatusName(s domain.CritiqueStatus) string {
	return string(s)
}
