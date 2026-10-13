//go:build !windows

package localapi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jufianto/sevjala/config"
)

const defaultEndpoint = "/var/run/sevjala.sock"

func customEndpoint(dataDir string) string {
	return filepath.Join(dataDir, "sevjala.sock")
}

// listen returns a warning when running as root without the sevjala group,
// in which case the socket stays root-only.
func listen(endpoint string) (net.Listener, string, error) {
	if c, err := net.DialTimeout("unix", endpoint, time.Second); err == nil {
		c.Close()
		return nil, "", ErrAlreadyRunning
	}
	if err := os.Remove(endpoint); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, "", err
	}
	ln, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, "", err
	}
	mode, warn := os.FileMode(0o600), ""
	if os.Geteuid() == 0 {
		if g, err := user.LookupGroup(config.SocketGroup); err == nil {
			gid, _ := strconv.Atoi(g.Gid)
			if err := os.Chown(endpoint, 0, gid); err != nil {
				ln.Close()
				return nil, "", err
			}
			mode = 0o660
		} else {
			warn = fmt.Sprintf("group %q not found; local API is root-only", config.SocketGroup)
		}
	}
	if err := os.Chmod(endpoint, mode); err != nil {
		ln.Close()
		return nil, "", err
	}
	return ln, warn, nil
}

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", endpoint)
}
