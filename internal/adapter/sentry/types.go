package sentry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

type sentryProject struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type sentryStringNumber string

func (v *sentryStringNumber) UnmarshalJSON(data []byte) error {
	if bytes.Equal(data, []byte("null")) {
		*v = ""
		return nil
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return err
	}

	switch value := raw.(type) {
	case string:
		*v = sentryStringNumber(value)
	case json.Number:
		*v = sentryStringNumber(value.String())
	default:
		return fmt.Errorf("decode sentry string/number: unsupported type %T", raw)
	}

	return nil
}

func (v sentryStringNumber) String() string {
	return string(v)
}

type sentryIssue struct {
	ID        string             `json:"id"`
	ShortID   string             `json:"shortId"`
	Title     string             `json:"title"`
	Culprit   string             `json:"culprit"`
	Permalink string             `json:"permalink"`
	Status    string             `json:"status"`
	Level     string             `json:"level"`
	Count     sentryStringNumber `json:"count"`
	UserCount sentryStringNumber `json:"userCount"`
	FirstSeen *sentryTime        `json:"firstSeen"`
	LastSeen  *sentryTime        `json:"lastSeen"`
	Project   sentryProject      `json:"project"`
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
