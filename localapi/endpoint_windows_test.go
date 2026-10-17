//go:build windows

package localapi

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func testIsAdmin() bool {
	var t windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &t); err != nil {
		return false
	}
	defer t.Close()
	return t.IsElevated()
}

func TestDefaultSDDL(t *testing.T) {
	for _, want := range []string{"O:BA", "(A;;GA;;;SY)", "(A;;GA;;;BA)", "(A;;0x12008b;;;IU)"} {
		if !strings.Contains(defaultSDDL, want) {
			t.Errorf("%s lacks %s", defaultSDDL, want)
		}
	}
	if _, err := windows.SecurityDescriptorFromString(defaultSDDL); err != nil {
		t.Errorf("%s: %v", defaultSDDL, err)
	}
}
