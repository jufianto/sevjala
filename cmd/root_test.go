package cmd

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		code   int
		stdout string
		stderr string
	}{
		{"version", []string{"version"}, 0, "sevjala dev (" + runtime.GOOS + "/" + runtime.GOARCH, ""},
		{"unknown command", []string{"nope"}, 2, "", "unknown command"},
		{"unknown flag", []string{"version", "--nope"}, 2, "", "unknown flag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(tt.args, &out, &errOut)
			if code != tt.code {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, tt.code, errOut.String())
			}
			if !strings.Contains(out.String(), tt.stdout) {
				t.Errorf("stdout = %q, want it to contain %q", out.String(), tt.stdout)
			}
			if !strings.Contains(errOut.String(), tt.stderr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tt.stderr)
			}
		})
	}
}

func TestExitCodeFromErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		code   int
		stderr string
	}{
		{"nil", nil, 0, ""},
		{"fail", failf("disk %s", "full"), 1, "sevjala: disk full\n"},
		{"usage", usagef("bad --role"), 2, "sevjala: bad --role\n"},
		{"silent", errSilent1, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errOut bytes.Buffer
			if got := exitCode(tt.err, &errOut); got != tt.code {
				t.Errorf("code = %d, want %d", got, tt.code)
			}
			if errOut.String() != tt.stderr {
				t.Errorf("stderr = %q, want %q", errOut.String(), tt.stderr)
			}
		})
	}
}

func TestHelpHidesTheServiceOnlyAgentCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"--help"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	if strings.Contains(out.String(), "\n  agent ") || !strings.Contains(out.String(), "\n  agents ") {
		t.Errorf("help must list agents, not agent:\n%s", out.String())
	}
}
