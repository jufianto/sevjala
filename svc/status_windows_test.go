package svc

import (
	"errors"
	"testing"

	"github.com/kardianos/service"
	"golang.org/x/sys/windows"
)

// A service's default DACL gives interactive users these rights (IU:
// CCLCSWLOCRRC), not start or stop; asking for more fails the whole query.
func TestServiceQueryAsksOnlyWhatUsersHave(t *testing.T) {
	const users = windows.SERVICE_QUERY_CONFIG | windows.SERVICE_QUERY_STATUS | windows.SERVICE_ENUMERATE_DEPENDENTS |
		windows.SERVICE_INTERROGATE | windows.SERVICE_USER_DEFINED_CONTROL | windows.READ_CONTROL
	if extra := uint32(queryAccess) &^ users; extra != 0 {
		t.Errorf("the status query asks for %#x, which a normal user does not have", extra)
	}
}

func TestQueryStatus(t *testing.T) {
	if st, err := queryStatus("EventLog"); err != nil || st != service.StatusRunning {
		t.Errorf("EventLog: %v, %v", st, err)
	}
	if _, err := queryStatus("sevjala-no-such-service"); !errors.Is(err, service.ErrNotInstalled) {
		t.Errorf("missing service: %v", err)
	}
}
