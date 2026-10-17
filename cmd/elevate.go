package cmd

import (
	"errors"
	"fmt"
	"io"
	"runtime"

	"github.com/jufianto/sevjala/svc"
)

// needsAdmin ends a command only an administrator may run; run starts the
// same command again elevated (AD-20).
type needsAdmin struct {
	why   string
	after func() // runs in this, unelevated, process once the elevated run succeeded
}

func (e *needsAdmin) Error() string { return e.why }

var errElevationRefused = errors.New("administrator rights were refused")

// runElevated runs this binary with args as administrator and waits for it.
var runElevated = elevated

func rerunElevated(args []string, na *needsAdmin, out, errOut io.Writer) int {
	if svc.IsAdmin() {
		fmt.Fprintf(errOut, "sevjala: %s\n", na.why)
		return 1
	}
	fmt.Fprintf(errOut, "%s; %s\n", na.why, elevationHow)
	code, err := runElevated(args)
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
