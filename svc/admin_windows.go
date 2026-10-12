//go:build windows

package svc

import "golang.org/x/sys/windows"

const AdminHint = "run this from an Administrator prompt"

// IsAdmin opens a real token: the GetCurrentProcessToken pseudo-handle is
// Windows 8+ and fails on Windows 7, which would make every check false.
func IsAdmin() bool {
	var t windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &t); err != nil {
		return false
	}
	defer t.Close()
	return t.IsElevated()
}
