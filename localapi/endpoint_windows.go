//go:build windows

package localapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net"
	"path/filepath"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

const defaultEndpoint = `\\.\pipe\sevjala`

// Owned by Administrators, which a normal user cannot set, so a client can
// tell the service's pipe from one a user created first. SYSTEM and
// Administrators have full control; interactive users read and write data
// but may not create pipe instances (blueprint security model, AD-20).
const defaultSDDL = "O:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x12008b;;;IU)"

// clientAccess is what the DACL grants interactive users.
const clientAccess = windows.GENERIC_READ | windows.FILE_WRITE_DATA

var errNotService = errors.New(`the program answering on \\.\pipe\sevjala is not the Sevjala service`)

func customEndpoint(dataDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dataDir)))
	return defaultEndpoint + "-" + hex.EncodeToString(sum[:4])
}

// Custom endpoints keep the default DACL so an un-elevated developer can
// reach the agent they started. A pipe that exists but refuses this user
// belongs to a running agent too.
func listen(endpoint string) (net.Listener, string, error) {
	timeout := time.Second
	if c, err := winio.DialPipe(endpoint, &timeout); err == nil || errors.Is(err, fs.ErrPermission) || errors.Is(err, winio.ErrTimeout) {
		if c != nil {
			c.Close()
		}
		return nil, "", ErrAlreadyRunning
	}
	cfg := &winio.PipeConfig{}
	if endpoint == defaultEndpoint {
		cfg.SecurityDescriptor = defaultSDDL
	}
	ln, err := winio.ListenPipe(endpoint, cfg)
	return ln, "", err
}

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	c, err := winio.DialPipeAccess(ctx, endpoint, clientAccess)
	if err != nil || endpoint != defaultEndpoint {
		return c, err
	}
	if !ownedByAdmins(handle(c)) {
		c.Close()
		return nil, errNotService
	}
	return c, nil
}

func handle(c net.Conn) windows.Handle {
	if f, ok := c.(interface{ Fd() uintptr }); ok {
		return windows.Handle(f.Fd())
	}
	return windows.InvalidHandle
}

func ownedByAdmins(h windows.Handle) bool {
	sd, err := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return false
	}
	owner, _, err := sd.Owner()
	return err == nil && (owner.IsWellKnown(windows.WinBuiltinAdministratorsSid) || owner.IsWellKnown(windows.WinLocalSystemSid))
}

// callerAdmin: an elevated process or SYSTEM is the administrator. Any
// failure to read the client's token makes it a normal caller.
func callerAdmin(c net.Conn) bool {
	var pid uint32
	if windows.GetNamedPipeClientProcessId(handle(c), &pid) != nil {
		return false
	}
	p, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(p) }()
	var t windows.Token
	if windows.OpenProcessToken(p, windows.TOKEN_QUERY, &t) != nil {
		return false
	}
	defer t.Close()
	if t.IsElevated() {
		return true
	}
	u, err := t.GetTokenUser()
	return err == nil && u.User.Sid.IsWellKnown(windows.WinLocalSystemSid)
}
