package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/svc"
)

func fakeElevation(t *testing.T, code int, err error) *[]string {
	t.Helper()
	if svc.IsAdmin() {
		t.Skip("runs as administrator")
	}
	var got []string
	old := runElevated
	runElevated = func(args []string) (int, error) { got = args; return code, err }
	t.Cleanup(func() { runElevated = old })
	return &got
}

func TestAdminOnlyCommandRunsAgainElevated(t *testing.T) {
	got := fakeElevation(t, 7, nil)
	args := []string{"service", "start", "--data-dir", t.TempDir()}
	var out, errOut bytes.Buffer
	if code := run(args, &out, &errOut); code != 7 {
		t.Errorf("exit %d, want the elevated run's 7; stderr %q", code, errOut.String())
	}
	if !slices.Equal(*got, args) {
		t.Errorf("elevated args %q, want %q", *got, args)
	}
	if !strings.Contains(errOut.String(), "needs administrator rights") || !strings.Contains(errOut.String(), elevationHow) {
		t.Errorf("stderr %q", errOut.String())
	}
}

func TestRefusedElevationChangesNothing(t *testing.T) {
	fakeElevation(t, 0, errElevationRefused)
	var out, errOut bytes.Buffer
	if code := run([]string{"service", "start", "--data-dir", t.TempDir()}, &out, &errOut); code != 1 ||
		!strings.Contains(errOut.String(), "Cancelled: administrator rights were refused") {
		t.Errorf("exit %d, stderr %q", code, errOut.String())
	}
}

func TestAgentAnswerAdminRequiredAsksForAdmin(t *testing.T) {
	g := &globals{dataDirFlag: t.TempDir()}
	_, err := callAgent(g, time.Second, func(context.Context, *localapi.Client) (struct{}, error) {
		return struct{}{}, localapi.AdminRequired("pairing a backup PC")
	})
	var na *needsAdmin
	if !errors.As(err, &na) || na.why != "pairing a backup PC needs administrator rights" {
		t.Errorf("got %#v", err)
	}
}

func TestCallAgentDenied(t *testing.T) {
	g := &globals{dataDirFlag: t.TempDir()}
	denied := &os.PathError{Op: "open", Path: `\\.\pipe\sevjala`, Err: fs.ErrPermission}
	_, err := callAgent(g, time.Second, func(context.Context, *localapi.Client) (struct{}, error) {
		return struct{}{}, fmt.Errorf("post: %w", denied)
	})
	var na *needsAdmin
	if runtime.GOOS == "windows" {
		if errors.As(err, &na) || !strings.Contains(err.Error(), svc.AdminHint) {
			t.Errorf("windows: got %v, want the admin hint (a service account cannot elevate)", err)
		}
		return
	}
	if !errors.As(err, &na) || !strings.Contains(na.why, "group sevjala") {
		t.Errorf("got %#v, want sudo with the group hint", err)
	}
}

func TestSetupAsksForAdminItself(t *testing.T) {
	got := fakeElevation(t, 0, nil)
	args := []string{"setup", "--data-dir", t.TempDir()}
	var out, errOut bytes.Buffer
	if code := run(args, &out, &errOut); code != 0 || !slices.Equal(*got, args) {
		t.Errorf("exit %d, elevated %q, stderr %q", code, *got, errOut.String())
	}
}

func TestAfterElevationRunsOnlyOnSuccess(t *testing.T) {
	for _, tt := range []struct {
		code int
		err  error
		want bool
	}{{0, nil, true}, {1, nil, false}, {0, errElevationRefused, false}} {
		fakeElevation(t, tt.code, tt.err)
		ran := false
		var out, errOut bytes.Buffer
		rerunElevated([]string{"setup"}, &needsAdmin{why: "x", after: func() { ran = true }}, &out, &errOut)
		if ran != tt.want {
			t.Errorf("exit %d, err %v: after ran %v", tt.code, tt.err, ran)
		}
	}
}
