package localapi

import (
	"errors"
	"net/http"
)

const reasonConfirmRequired = "confirm_required"

// ConfirmRequired is the 409 a destructive request answers until it carries
// confirm=word; deletes and hosts say what it would delete, for the prompt.
func ConfirmRequired(msg, word string, deletes int, hosts []string) error {
	return &Error{Code: http.StatusConflict, Msg: msg, Reason: reasonConfirmRequired, Word: word, Deletes: deletes, Hosts: hosts}
}

func AsConfirm(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) && e.Reason == reasonConfirmRequired {
		return e, true
	}
	return nil, false
}
