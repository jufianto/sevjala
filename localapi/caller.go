package localapi

import (
	"context"
	"errors"
	"net/http"
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

func hasCaller(ctx context.Context) bool {
	_, ok := ctx.Value(callerKey{}).(bool)
	return ok
}

const reasonAdminRequired = "admin_required"

// AdminRequired is the 403 an admin-only action answers other callers; the
// CLI re-runs the command elevated when it sees it.
func AdminRequired(action string) error {
	return &Error{Code: http.StatusForbidden, Msg: action + " needs administrator rights", Reason: reasonAdminRequired}
}

func IsAdminRequired(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Reason == reasonAdminRequired
}
