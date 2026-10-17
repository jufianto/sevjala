package cmd

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/ui"
	"github.com/jufianto/sevjala/uiproxy"
)

// uiIdle is how long the dashboard keeps running with no tab open.
var uiIdle = time.Minute

var openBrowser = func(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	}
	return exec.Command("xdg-open", url).Start()
}

func newUICmd(g *globals) *cobra.Command {
	var noBrowser bool
	c := &cobra.Command{
		Use:   "ui",
		Short: "Open the dashboard in your browser",
		Long: `Opens the Sevjala dashboard in your browser: whether the data is protected, the databases
and the backup PCs drawn as a diagram, updating by itself. Only this PC can open it
(127.0.0.1, with a link that works once); it needs no administrator rights and stops by
itself a minute after its browser tab is closed.`,
		Example: `  sevjala ui
  sevjala ui --no-browser      (print the link instead of opening a browser)`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return fail(err)
			}
			s, err := uiproxy.New(localapi.Endpoint(dataDir), ui.FS(), ln)
			if err != nil {
				return fail(err)
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			fmt.Fprintf(c.OutOrStdout(), "Dashboard: %s\nIt stops by itself a minute after its tab is closed; Ctrl-C stops it now.\n", s.URL())
			if !noBrowser {
				if err := openBrowser(s.URL()); err != nil {
					fmt.Fprintf(c.ErrOrStderr(), "could not open a browser (%v); open the link above\n", err)
				}
			}
			if err := s.Serve(ctx, uiIdle); err != nil {
				return fail(err)
			}
			return nil
		},
	}
	c.Flags().BoolVar(&noBrowser, "no-browser", false, "print the link instead of opening a browser")
	return c
}
