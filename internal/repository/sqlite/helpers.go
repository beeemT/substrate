// Package sqlite provides SQLite implementations of the repository interfaces.
package sqlite

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

func parseTime(s string) (time.Time, error) {
	t, err := domain.ParseTime(s)
	if err == nil {
		return t, nil
	}
	// Fall back to RFC3339Nano
	t, err = time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}

	return t, nil
}

func parseTimePtr(s *string) (*time.Time, error) {
	if s == nil {
		return nil, nil
	}
	t, err := parseTime(*s)
	if err != nil {
		return nil, err
	}

	return &t, nil
}

func formatTime(t time.Time) string {
	return domain.FormatTime(t)
}

func formatTimePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := formatTime(*t)

	return &s
}

func marshalJSON(v any) (*string, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal JSON: %w", err)
	}
	s := string(b)

	return &s, nil
}

func marshalStringSlice(v []string) (*string, error) {
	if len(v) == 0 {
		return nil, nil
	}

	return marshalJSON(v)
}

func marshalMap(v map[string]any) (*string, error) {
	if len(v) == 0 {
		return nil, nil
	}

	return marshalJSON(v)
}

func unmarshalStringSlice(s *string) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	var v []string
	if err := json.Unmarshal([]byte(*s), &v); err != nil {
		return nil, err
	}

	return v, nil
}

func unmarshalMap(s *string) (map[string]any, error) {
	if s == nil {
		return nil, nil
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(*s), &v); err != nil {
		return nil, err
	}

	return v, nil
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}

func derefStr(s *string) string {
	if s == nil {
		return ""
	}

	return *s
}
