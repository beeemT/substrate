package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/beeemT/substrate/internal/domain"
	"github.com/beeemT/substrate/internal/repository"
)

func PersistReviewArtifact(ctx context.Context, eventRepo repository.EventRepository, workspaceID, workItemID string, artifact domain.ReviewArtifact) error {
	if eventRepo == nil || strings.TrimSpace(workspaceID) == "" || strings.TrimSpace(workItemID) == "" {
		return nil
	}
	payload, err := json.Marshal(domain.ReviewArtifactEventPayload{WorkItemID: workItemID, Artifact: artifact})
	if err != nil {
		return fmt.Errorf("marshal review artifact payload: %w", err)
	}
	createdAt := artifact.UpdatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}

	return eventRepo.Create(ctx, domain.SystemEvent{
		ID:          domain.NewID(),
		EventType:   string(domain.EventReviewArtifactRecorded),
		WorkspaceID: workspaceID,
		Payload:     string(payload),
		CreatedAt:   createdAt,
	})
}
