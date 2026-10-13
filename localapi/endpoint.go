package localapi

import (
	"errors"

	"github.com/jufianto/sevjala/config"
)

// ErrAlreadyRunning: another agent answers on the endpoint, usually the service.
var ErrAlreadyRunning = errors.New("another Sevjala agent is already running here (the service runs it; see 'sevjala service status')")

// Endpoint is the pipe or socket for a data dir. A non-default data dir gets
// its own endpoint so dev and test agents never collide with the service.
func Endpoint(dataDir string) string {
	if config.IsDefaultDataDir(dataDir) {
		return defaultEndpoint
	}
	return customEndpoint(dataDir)
}
