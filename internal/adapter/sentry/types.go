package sentry

import (
	"encoding/json"
	"time"
)

type sentryProject struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type sentryIssue struct {
	ID        string        `json:"id"`
	ShortID   string        `json:"shortId"`
	Title     string        `json:"title"`
	Culprit   string        `json:"culprit"`
	Permalink string        `json:"permalink"`
	Status    string        `json:"status"`
	Level     string        `json:"level"`
	Count     string        `json:"count"`
	UserCount string        `json:"userCount"`
	FirstSeen *sentryTime   `json:"firstSeen"`
	LastSeen  *sentryTime   `json:"lastSeen"`
	Project   sentryProject `json:"project"`
}

type sentryTime struct {
	time.Time
}

func (t *sentryTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		t.Time = time.Time{}
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw == "" {
		t.Time = time.Time{}
		return nil
	}
	parsed, err := parseSentryTime(raw)
	if err != nil {
		return err
	}
	t.Time = parsed
	return nil
}
