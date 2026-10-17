package cmd

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/svc"
	"github.com/jufianto/sevjala/tray"
)

func newTrayCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "tray",
		Short: "Show the status icon: green, yellow or red (starts at login)",
		Long: `Shows Sevjala's status as an icon next to the clock: green when the data is protected,
yellow when something needs a look, red for a problem, grey when the service does not
answer. Its menu shows the status lines, "Back up now", "Open dashboard" and "Open status". Setup starts
it at every login; it needs no administrator rights.`,
		Example: `  sevjala tray`,
		Args:    cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			exe, err := os.Executable()
			if err != nil {
				return fail(err)
			}
			if err := tray.Run(localapi.NewClient(localapi.Endpoint(dataDir)), exe, svc.TrayAutostartInstalled); err != nil {
				return fail(err)
			}
			return nil
		},
	}
}
