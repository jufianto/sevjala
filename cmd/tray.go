package cmd

import (
	"errors"
	"os"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/svc"
	"github.com/jufianto/sevjala/tray"
)

var (
	runTray       = tray.Run
	openDashboard = tray.Dashboard
)

func newTrayCmd(g *globals) *cobra.Command {
	var show bool
	c := &cobra.Command{
		Use:   "tray",
		Short: "Show the status icon: green, yellow or red (starts at login)",
		Long: `Shows Sevjala's status as an icon next to the clock: green when the data is protected,
yellow when something needs a look, red for a problem, grey when the service does not
answer. Its menu shows the status lines, "Back up now", "Open dashboard" and "Open status". Setup starts
it at every login, and on Windows adds "Sevjala" to the Start menu, which starts it again after
"Quit tray" (or opens the dashboard when it runs). It needs no administrator rights.`,
		Example: `  sevjala tray
  sevjala tray --show      (open the dashboard if the tray already runs)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			exe, err := os.Executable()
			if err != nil {
				return fail(err)
			}
			err = runTray(localapi.NewClient(localapi.Endpoint(dataDir)), exe, svc.TrayAutostartInstalled)
			switch {
			case errors.Is(err, tray.ErrAlreadyRunning) && show:
				err = openDashboard(exe)
			case errors.Is(err, tray.ErrAlreadyRunning):
				err = nil
			}
			if err != nil {
				return fail(err)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&show, "show", false, "if the tray already runs, open the dashboard instead")
	return c
}
