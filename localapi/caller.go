package localapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/jufianto/sevjala/logx"
)

type callerKey struct{}

// WithAdmin records whether the caller of a request is an administrator
// (elevated on Windows, root on Unix); the server sets it per connection.
func WithAdmin(ctx context.Context, admin bool) context.Context {
	return context.WithValue(ctx, callerKey{}, admin)
}

// CallerIsAdmin is false when the caller is unknown.
func CallerIsAdmin(ctx context.Context) bool {
	admin, _ := ctx.Value(callerKey{}).(bool)
	return admin
}

// CallerAccount names the account a request came from, for the log.
func CallerAccount(ctx context.Context) string { return logx.By(ctx) }

func hasCaller(ctx context.Context) bool {
	_, ok := ctx.Value(callerKey{}).(bool)
	return ok
}

const (
	reasonAdminRequired           = "admin_required"
	reasonControllerAdminRequired = "controller_admin_required"
)

// AdminRequired is the 403 an admin-only action answers other callers; the
// CLI re-runs the command elevated when it sees it. From a manager it names
// the controller instead, where elevating the manager would not help.
func AdminRequired(ctx context.Context, action string) error {
	if v, ok := ViaOf(ctx); ok {
		return &Error{Code: http.StatusForbidden, Msg: action + " needs administrator rights on " + v.Controller + " itself",
			Reason: reasonControllerAdminRequired}
	}
	return &Error{Code: http.StatusForbidden, Msg: action + " needs administrator rights", Reason: reasonAdminRequired}
}

func IsAdminRequired(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Reason == reasonAdminRequired
}

// Via marks a request that came over a manager link (AD-21): Manager is the
// node it came from, Controller this controller's hostname.
type Via struct{ Manager, Controller string }

type viaKey struct{}

func WithVia(ctx context.Context, v Via) context.Context { return context.WithValue(ctx, viaKey{}, v) }

func ViaOf(ctx context.Context) (Via, bool) {
	v, ok := ctx.Value(viaKey{}).(Via)
	return v, ok
}
