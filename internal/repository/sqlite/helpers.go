// Package sqlite provides SQLite implementations of the repository interfaces.
package sqlite

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/beeemT/substrate/internal/domain"
)

var sqliteTimestampLayouts = [...]string{
	domain.TimeFormat,
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
}

func parseTime(s string) (time.Time, error) {
	var parseErr error
	for _, layout := range sqliteTimestampLayouts {
		t, err := time.Parse(layout, s)
		if err == nil {
			return t, nil
		}
		parseErr = err
	}

	return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, parseErr)
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
