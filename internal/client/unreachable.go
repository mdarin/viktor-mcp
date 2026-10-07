package client

import (
	"context"
	"errors"
	"net"
	httpurl "net/url"
	"syscall"
)

// ErrUnreachable marks connection-level failures: the Vikunja API could not
// be contacted at all (connection refused, DNS failure, TLS handshake, dial
// timeout). HTTP 4xx/5xx answers from the server are NOT unreachable — the
// server was reached and rejected the request.
var ErrUnreachable = errors.New("vikunja unreachable")

// UnreachableError wraps a transport error and matches errors.Is(err,
// ErrUnreachable).
type UnreachableError struct {
	Err error
}

func (e *UnreachableError) Error() string { return ErrUnreachable.Error() + ": " + e.Err.Error() }
func (e *UnreachableError) Unwrap() []error {
	return []error{ErrUnreachable, e.Err}
}

// classifyUnreachable wraps transport errors as ErrUnreachable unless the
// caller's own context was cancelled or timed out.
func classifyUnreachable(ctx context.Context, err error) error {
	if err == nil || ctx.Err() != nil {
		return err
	}
	if IsUnreachable(err) {
		return &UnreachableError{Err: err}
	}
	return err
}

// IsUnreachable reports whether err is (or wraps) a connection-level failure.
func IsUnreachable(err error) bool {
	if errors.Is(err, ErrUnreachable) {
		return true
	}
	var uErr *httpurl.Error
	if errors.As(err, &uErr) {
		return classifyTransport(uErr.Err)
	}
	return classifyTransport(err)
}

func classifyTransport(err error) bool {
	if errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, syscall.ENETUNREACH) {
		return true
	}
	var dErr *net.DNSError
	if errors.As(err, &dErr) {
		return true
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var cErr *net.UnknownNetworkError
	return errors.As(err, &cErr)
}
