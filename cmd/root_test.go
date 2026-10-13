package cmd

import (
	"bytes"
	"runtime"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

// Operators learn the CLI from --help: every command a user can run explains
// itself with an example, and the top level is grouped by task.
func TestEveryCommandHasHelpWithExamples(t *testing.T) {
	root := newRootCmd()
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if sub.Hidden || sub.Name() == "help" {
				continue
			}
			if sub.Short == "" {
				t.Errorf("%s: no short description", sub.CommandPath())
			}
			if sub.Runnable() && sub.Example == "" {
				t.Errorf("%s: no example", sub.CommandPath())
			}
			if c == root && sub.GroupID == "" {
				t.Errorf("%s: not in a help group", sub.CommandPath())
			}
			for _, jargon := range []string{"mutual TLS", "reconciliation", "data dir"} {
				if strings.Contains(sub.Short, jargon) {
					t.Errorf("%s: %q in %q", sub.CommandPath(), jargon, sub.Short)
				}
			}
			walk(sub)
		}
	}
	walk(root)
}
