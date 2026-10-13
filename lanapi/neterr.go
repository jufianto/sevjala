package lanapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
)

var errNoCert = errors.New("server sent no certificate")

// lanError is a connection failure worded for an office operator; the cause
// stays reachable for errors.Is and errors.As.
type lanError struct {
	msg string
	err error
}

func (e *lanError) Error() string { return e.msg }
func (e *lanError) Unwrap() error { return e.err }

// plain words a failed request to the agent at addr without URL, method or OS jargon.
func plain(addr string, err error) error {
	var dns *net.DNSError
	var msg string
	switch {
	case errors.Is(err, ErrPinMismatch):
		msg = "this node's identity changed since pairing (reinstalled, or another PC now has its address): pair it again"
	case errors.As(err, &dns) && dns.IsNotFound:
		msg = fmt.Sprintf("cannot find '%s' on the network; use its IP address instead", dns.Name)
	case errors.Is(err, context.DeadlineExceeded) || isAny(err, timeoutErrs) || inChain(err, timedOut):
		msg = addr + " did not answer in time: is that PC on and on this network?"
	case isAny(err, refusedErrs):
		msg = "nothing answers at " + addr + ": is Sevjala installed and running on that PC?"
	case isAny(err, unreachableErrs):
		msg = "cannot reach " + addr + " from this PC: it is on another network, or the router keeps devices apart (client/AP isolation)"
	case errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || isAny(err, resetErrs):
		msg = addr + " closed the connection: try again"
	case errors.Is(err, errNoCert) || errors.Is(err, http.ErrSchemeMismatch) || inChain(err, func(e error) bool { return strings.HasPrefix(e.Error(), "tls: ") }):
		msg = addr + " is not a Sevjala agent"
	default:
		inner := err
		for u := errors.Unwrap(inner); u != nil; u = errors.Unwrap(u) {
			inner = u
		}
		msg = addr + ": " + inner.Error()
	}
	return &lanError{msg: msg, err: err}
}

func isAny(err error, targets []error) bool {
	for _, t := range targets {
		if errors.Is(err, t) {
			return true
		}
	}
	return false
}

func inChain(err error, f func(error) bool) bool {
	for ; err != nil; err = errors.Unwrap(err) {
		if f(err) {
			return true
		}
	}
	return false
}

func timedOut(err error) bool {
	t, ok := err.(interface{ Timeout() bool })
	return ok && t.Timeout()
}
