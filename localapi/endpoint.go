package localapi

import "github.com/jufianto/sevjala/config"

// Endpoint is the pipe or socket for a data dir. A non-default data dir gets
// its own endpoint so dev and test agents never collide with the service.
func Endpoint(dataDir string) string {
	if config.IsDefaultDataDir(dataDir) {
		return defaultEndpoint
	}
	return customEndpoint(dataDir)
}
