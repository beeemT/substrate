package sqlite

import (
	"context"
	stderrors "errors"
	"fmt"
	"net"
	"os"
	"time"

	"go.uber.org/multierr"
)

const (
	sqlitePrimaryCodeMask = 0xff
	sqliteBusy            = 5 // SQLITE_BUSY
	sqliteLocked          = 6 // SQLITE_LOCKED
)

// eventRetryBackoffs are the backoffs used when retrying event persistence due to SQLITE_BUSY.
var eventRetryBackoffs = []time.Duration{
	100 * time.Millisecond,
	time.Second,
	5 * time.Second,
	5 * time.Second,
	10 * time.Second,
}

// sqliteRetry retries transient transaction failures, including SQLite extended
// busy/locked result codes such as SQLITE_BUSY_SNAPSHOT (517). modernc.org/sqlite
// exposes extended result codes from Code(), while go-atomic only matches primary
// codes 5 and 6, so repository transacters need a SQLite-aware classifier.
func sqliteRetry(backoffs []time.Duration, run func() error) error {
	var (
		i    int
		merr error
	)

	err := run()
	for i = 0; isRetryableTransactionError(err) && i < len(backoffs); i++ {
		merr = multierr.Append(merr, fmt.Errorf("try %d: %w", i, err))
		if sleep := backoffs[i]; sleep > 0 {
			time.Sleep(sleep)
		}
		err = run()
	}
	if err != nil {
		merr = multierr.Append(merr, fmt.Errorf("try %d: %w", i, err))
		return fmt.Errorf("error not retryable or reached maximum number of retries: %w", merr)
	}
	return nil
}

func isRetryableTransactionError(err error) bool {
	if err == nil {
		return false
	}
	if stderrors.Is(err, context.DeadlineExceeded) || stderrors.Is(err, net.ErrClosed) || stderrors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	return isSQLiteBusyOrLocked(err)
}

// isSQLiteBusyOrLocked checks if an error is a retryable SQLite busy/locked error.
func isSQLiteBusyOrLocked(err error) bool {
	if err == nil {
		return false
	}
	var sqliteErr interface{ Code() int }
	if stderrors.As(err, &sqliteErr) {
		switch sqliteErr.Code() & sqlitePrimaryCodeMask {
		case sqliteBusy, sqliteLocked:
			return true
		}
	}
	return false
}
