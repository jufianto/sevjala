package svc

import (
	"bytes"
	"fmt"
	"os/exec"
	"os/user"
	"runtime"
	"strings"

	"github.com/jufianto/sevjala/config"
)

// EnsureGroup creates the local API socket group and adds user to it.
// It does nothing on Windows, where the pipe ACL replaces the group.
func EnsureGroup(username string) error {
	_, err := user.LookupGroup(config.SocketGroup)
	for _, c := range groupCommands(runtime.GOOS, err == nil, username) {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(c, " "), err, bytes.TrimSpace(out))
		}
	}
	return nil
}

func groupCommands(goos string, exists bool, username string) [][]string {
	g := config.SocketGroup
	var cmds [][]string
	switch goos {
	case "linux":
		if !exists {
			cmds = append(cmds, []string{"groupadd", "--system", g})
		}
		if username != "" {
			cmds = append(cmds, []string{"usermod", "-aG", g, username})
		}
	case "darwin":
		if !exists {
			cmds = append(cmds, []string{"dseditgroup", "-o", "create", "-r", "Sevjala", g})
		}
		if username != "" {
			cmds = append(cmds, []string{"dseditgroup", "-o", "edit", "-a", username, "-t", "user", g})
		}
	}
	return cmds
}
