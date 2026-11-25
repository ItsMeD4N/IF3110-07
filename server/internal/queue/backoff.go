package queue

import (
	"context"
	"database/sql/driver"
	"errors"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"strings"
	"time"

	"github.com/lib/pq"
)

type BackoffPolicy struct {
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	MaxAttempts int
	JitterRatio float64
}

func DefaultBackoffPolicy() BackoffPolicy {
	return BackoffPolicy{
		BaseDelay:   1 * time.Second,
		MaxDelay:    30 * time.Second,
		MaxAttempts: 3,
		JitterRatio: 0.2,
	}
}

func (p BackoffPolicy) NextDelay(attempt int) time.Duration {
	base := p.BaseDelay
	if base <= 0 {
		base = time.Second
	}
	maxD := p.MaxDelay
	if maxD <= 0 {
		maxD = 30 * time.Second
	}
	if attempt < 1 {
		attempt = 1
	}
	exp := math.Pow(2, float64(attempt-1))
	d := time.Duration(float64(base) * exp)
	if d > maxD || d < 0 {
		d = maxD
	}
	jitter := p.JitterRatio
	if jitter > 0 {
		if jitter > 0.5 {
			jitter = 0.5
		}
		factor := (1.0 - jitter) + rand.Float64()*(2.0*jitter)
		d = time.Duration(float64(d) * factor)
	}
	if d < 10*time.Millisecond {
		d = 10 * time.Millisecond
	}
	return d
}

type TransientError struct{ Err error }

func (e *TransientError) Error() string { return e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

func MarkTransient(err error) error {
	if err == nil {
		return nil
	}
	return &TransientError{Err: err}
}

func IsTransientError(err error) bool {
	if err == nil {
		return false
	}
	var te *TransientError
	if errors.As(err, &te) {
		return true
	}
	if errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		code := string(pqErr.Code)
		switch {
		case strings.HasPrefix(code, "08"):
			return true
		case code == "40001":
			return true
		case code == "40P01":
			return true
		case strings.HasPrefix(code, "53"):
			return true
		case code == "55P03":
			return true
		case code == "57P01", code == "57P02", code == "57P03", code == "57P04":
			return true
		default:
			return false
		}
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range []string{
		"connection reset",
		"broken pipe",
		"connection refused",
		"i/o timeout",
		"unexpected eof",
		"bad connection",
		"server closed the connection",
		"cannot connect now",
		"too many connections",
	} {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

func IsUncertainCommitError(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		code := string(pqErr.Code)
		return strings.HasPrefix(code, "08") || code == "57P02"
	}
	return IsTransientError(err)
}
