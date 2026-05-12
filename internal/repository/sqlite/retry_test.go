package sqlite

import (
	"fmt"
	"testing"
	"time"
)

type codedError int

func (e codedError) Error() string { return fmt.Sprintf("sqlite code %d", int(e)) }
func (e codedError) Code() int     { return int(e) }

func TestIsSQLiteBusyOrLockedIncludesExtendedCodes(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		code int
	}{
		{name: "busy", code: sqliteBusy},
		{name: "locked", code: sqliteLocked},
		{name: "busy snapshot", code: 517},
		{name: "locked shared cache", code: 262},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if !isSQLiteBusyOrLocked(codedError(tc.code)) {
				t.Fatalf("code %d should be retryable", tc.code)
			}
		})
	}
}

func TestSQLiteRetryRetriesExtendedBusySnapshot(t *testing.T) {
	t.Parallel()

	calls := 0
	err := sqliteRetry([]time.Duration{0}, func() error {
		calls++
		if calls == 1 {
			return codedError(517)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("sqliteRetry returned error: %v", err)
	}
	if calls != 2 {
		t.Fatalf("sqliteRetry calls = %d, want 2", calls)
	}
}

func TestSQLiteRetryDoesNotRetryNonRetryableError(t *testing.T) {
	t.Parallel()

	calls := 0
	err := sqliteRetry([]time.Duration{0}, func() error {
		calls++
		return codedError(1)
	})
	if err == nil {
		t.Fatal("sqliteRetry returned nil for non-retryable error")
	}
	if calls != 1 {
		t.Fatalf("sqliteRetry calls = %d, want 1", calls)
	}
}
