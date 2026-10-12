package svc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/jufianto/sevjala/config"
)

// PrepareDataDir creates the data and log dirs and writes a default config
// for role when none exists. It never rewrites an existing config.
func PrepareDataDir(dataDir, role string) (created bool, err error) {
	if err := os.MkdirAll(config.LogDir(dataDir), 0o755); err != nil {
		return false, err
	}
	path := config.Path(dataDir)
	cur, err := config.Load(path)
	switch {
	case err == nil:
		if cur.Role != role {
			return false, fmt.Errorf("%s already has role %q; refusing to install as %q", path, cur.Role, role)
		}
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		return true, config.Write(path, config.Default(role))
	default:
		return false, err
	}
}
