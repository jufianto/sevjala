//go:build windows

package svc

import (
	"errors"
	"fmt"

	"github.com/kardianos/service"
	"golang.org/x/sys/windows"
)

// queryAccess is all a status query needs. The service library also asks for
// start and stop, which only administrators have, so its query fails for the
// normal users that 'sevjala status' and the tray run as.
const queryAccess = windows.SERVICE_QUERY_STATUS

func queryStatus(name string) (service.Status, error) {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return service.StatusUnknown, err
	}
	defer func() { _ = windows.CloseServiceHandle(m) }()
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return service.StatusUnknown, err
	}
	h, err := windows.OpenService(m, n, queryAccess)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return service.StatusUnknown, service.ErrNotInstalled
	}
	if err != nil {
		return service.StatusUnknown, err
	}
	defer func() { _ = windows.CloseServiceHandle(h) }()
	var s windows.SERVICE_STATUS
	if err := windows.QueryServiceStatus(h, &s); err != nil {
		return service.StatusUnknown, err
	}
	switch s.CurrentState {
	case windows.SERVICE_START_PENDING, windows.SERVICE_RUNNING:
		return service.StatusRunning, nil
	case windows.SERVICE_PAUSE_PENDING, windows.SERVICE_PAUSED, windows.SERVICE_CONTINUE_PENDING,
		windows.SERVICE_STOP_PENDING, windows.SERVICE_STOPPED:
		return service.StatusStopped, nil
	}
	return service.StatusUnknown, fmt.Errorf("unknown service state %d", s.CurrentState)
}

// statusOnly answers Status with the query a normal user is allowed.
type statusOnly struct{ service.Service }

func (s statusOnly) Status() (service.Status, error) { return queryStatus(Name) }

func withUserStatus(s service.Service) service.Service { return statusOnly{s} }
