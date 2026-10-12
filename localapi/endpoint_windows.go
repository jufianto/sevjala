//go:build windows

package localapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"path/filepath"

	"github.com/Microsoft/go-winio"
)

const defaultEndpoint = `\\.\pipe\sevjala`

// Administrators and SYSTEM only (blueprint security model, Local API row).
const defaultSDDL = "D:P(A;;GA;;;BA)(A;;GA;;;SY)"

func customEndpoint(dataDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dataDir)))
	return defaultEndpoint + "-" + hex.EncodeToString(sum[:4])
}

// Custom endpoints keep the default DACL so an un-elevated developer can
// reach the agent they started.
func listen(endpoint string) (net.Listener, string, error) {
	cfg := &winio.PipeConfig{}
	if endpoint == defaultEndpoint {
		cfg.SecurityDescriptor = defaultSDDL
	}
	ln, err := winio.ListenPipe(endpoint, cfg)
	return ln, "", err
}

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
}
