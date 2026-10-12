package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/localapi"
)

type serviceStatuser interface {
	Status() (service.Status, error)
	Platform() string
}

type statusView struct {
	Service   string
	ServiceUp bool
	Platform  string
	Agent     *localapi.Status
	AgentErr  error
	DataDir   string
}

func (v statusView) OK() bool { return v.ServiceUp && v.Agent != nil }

// buildStatusView treats an answering agent as proof the service runs when an
// unprivileged caller cannot see the service manager's state (macOS launchctl).
func buildStatusView(s serviceStatuser, st localapi.Status, apiErr error, dataDir string, privileged bool) statusView {
	v := statusView{Platform: s.Platform(), DataDir: dataDir}
	if apiErr == nil {
		v.Agent = &st
	} else {
		v.AgentErr = apiErr
	}
	state, err := s.Status()
	switch {
	case errors.Is(err, service.ErrNotInstalled):
		v.Service = "not installed"
	case err != nil:
		v.Service = "unknown (" + err.Error() + ")"
	case state == service.StatusRunning:
		v.Service, v.ServiceUp = "running", true
	case !privileged && v.Agent != nil:
		v.Service, v.ServiceUp = "unknown (run with sudo for the service state)", true
	default:
		v.Service = "stopped"
	}
	return v
}

func renderStatus(w io.Writer, v statusView) {
	fmt.Fprintf(w, "%-10s%-12s (%s)\n", "service", v.Service, v.Platform)
	if v.Agent != nil {
		a := v.Agent
		fmt.Fprintf(w, "%-10s%-12s %s  role=%s  up %s  pid %d\n", "agent", "answering",
			a.Version, a.Role, time.Duration(a.UptimeS)*time.Second, a.PID)
	} else {
		fmt.Fprintf(w, "%-10snot answering (%v)\n", "agent", v.AgentErr)
	}
	fmt.Fprintf(w, "%-10s%s\n", "data dir", v.DataDir)
}

func queryAgent(c *localapi.Client) (localapi.Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return c.Status(ctx)
}

func waitForAgent(c *localapi.Client, timeout time.Duration) (localapi.Status, error) {
	deadline := time.Now().Add(timeout)
	for {
		st, err := queryAgent(c)
		if err == nil || time.Now().After(deadline) {
			return st, err
		}
		time.Sleep(250 * time.Millisecond)
	}
}
