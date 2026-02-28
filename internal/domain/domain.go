// Package domain contains the core domain model structs for Substrate.
package domain

import (
	"crypto/rand"
	"time"

	"github.com/oklog/ulid/v2"
)

// TimeFormat is the ISO 8601 format used for all DB-stored timestamps.
const TimeFormat = "2006-01-02T15:04:05.000Z"

// NewID generates a new ULID string.
func NewID() string {
	return ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// Now returns the current time in UTC.
func Now() time.Time {
	return time.Now().UTC()
}

// FormatTime formats a time.Time for DB storage.
func FormatTime(t time.Time) string {
	return t.UTC().Format(TimeFormat)
}

// ParseTime parses a DB-stored timestamp string.
func ParseTime(s string) (time.Time, error) {
	return time.Parse(TimeFormat, s)
}
