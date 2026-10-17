//go:build !(windows || linux || (darwin && cgo && tray))

package tray

import (
	"errors"

	"github.com/jufianto/sevjala/localapi"
)

const Available = false

func Run(*localapi.Client, string, func() bool) error {
	return errors.New("this build has no tray; on macOS build it with CGO_ENABLED=1 and -tags tray")
}

func Start(string) error { return Run(nil, "", nil) }

func Dashboard(string) error { return Run(nil, "", nil) }
