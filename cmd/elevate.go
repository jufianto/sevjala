package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/svc"
)

// needsAdmin ends a command only an administrator may run; run starts the
// same command again elevated (AD-20).
type needsAdmin struct {
	why   string
	after func() // runs in this, unelevated, process once the elevated run succeeded
}

func (e *needsAdmin) Error() string { return e.why }

// elevatedMarker starts the elevated run, so one that still lacks the rights stops.
const elevatedMarker = "--elevated-rerun"

var errElevationRefused = errors.New("administrator rights were refused")

// runElevated runs this binary with args as administrator and waits for it.
var runElevated = elevated

func rerunElevated(args []string, na *needsAdmin, out, errOut io.Writer) int {
	switch {
	case slices.Contains(args, elevatedMarker):
		fmt.Fprintf(errOut, "sevjala: %s; this account could not get administrator rights: sign in as an administrator\n", na.why)
		return 1
	case svc.IsAdmin():
		fmt.Fprintf(errOut, "sevjala: %s\n", na.why)
		return 1
	}
	fmt.Fprintf(errOut, "%s; %s\n", na.why, elevationHow)
	rerun := []string{elevatedMarker}
	if dir := os.Getenv(config.EnvDataDir); dir != "" {
		if abs, err := filepath.Abs(dir); err == nil {
			rerun = append(rerun, "--data-dir="+abs)
		}
	}
	code, err := runElevated(append(rerun, args...))
	switch {
	case errors.Is(err, errElevationRefused):
		fmt.Fprintln(errOut, "Cancelled: administrator rights were refused; nothing was changed.")
		return 1
	case err != nil:
		fmt.Fprintf(errOut, "sevjala: %v\n", err)
		return 1
	}
	if runtime.GOOS == "windows" {
		fmt.Fprintf(out, "finished in the administrator window (exit %d)\n", code)
	}
	if code == 0 && na.after != nil {
		na.after()
	}
	return code
}
