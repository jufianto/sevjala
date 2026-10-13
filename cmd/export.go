package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
)

func newBackupsExportCmd(g *globals) *cobra.Command {
	var db, to string
	var force bool
	c := &cobra.Command{
		Use:   "export <backup|latest>",
		Short: "Copy a backup this PC holds into a folder you can open",
		Long: `Copies one backup out of Sevjala's locked folder into <FOLDER>/<database>/, named by when
it was made: 2026-10-14_00-20-00.sql.gz (the dump) and 2026-10-14_00-20-00.json (what it
is and its sha256). The copy is checked against the sha256 while it is written, and
belongs to you, not to the service. Sevjala's own copy stays where it is.

Works on a backup PC (what 'sevjala backups list' shows there) and on the controller
(only the newest backups, which it keeps in staging). An existing file is never replaced
unless you add --force.`,
		Example: `  sevjala backups export latest --to ~/Desktop
  sevjala backups export latest --db shop --to D:\restore
  sevjala backups export BK-20261014-002000-30D3        (into the current folder)`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			held, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]inventory.Manifest, error) {
				return cl.Held(ctx)
			})
			if err != nil {
				return err
			}
			m, err := pickHeld(held, args[0], db)
			if err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			body, err := localapi.NewClient(localapi.Endpoint(dataDir)).OpenHeld(ctx, m.BackupID)
			if err != nil {
				return fail(agentProblem(err, nil))
			}
			defer body.Close()
			path, err := writeExport(m, body, to, force)
			if err != nil {
				return err
			}
			if abs, err := filepath.Abs(path); err == nil {
				path = abs
			}
			fmt.Fprintf(c.OutOrStdout(), "exported %s (%s, %s, %s), sha256 checked\n  %s\n  %s\n", m.BackupID, m.Database,
				m.CreatedAt.Local().Format("2006-01-02 15:04"), inventory.HumanBytes(m.Size),
				path, strings.TrimSuffix(path, inventory.DataExt)+inventory.ManifestExt)
			return nil
		},
	}
	c.Flags().StringVar(&db, "db", "", "with latest: the newest backup of database `NAME`")
	c.Flags().StringVar(&to, "to", ".", "`FOLDER` to copy into; it must exist")
	c.Flags().BoolVar(&force, "force", false, "replace files with the same name in that folder")
	return c
}

// pickHeld finds the backup the export names: an id, or "latest" (of db).
func pickHeld(held []inventory.Manifest, arg, db string) (inventory.Manifest, error) {
	if arg != "latest" {
		if i := slices.IndexFunc(held, func(m inventory.Manifest) bool { return m.BackupID == arg }); i >= 0 {
			return held[i], nil
		}
		return inventory.Manifest{}, failf("%s is not on this PC; 'sevjala backups list' on a backup PC shows what it holds (the controller keeps only its newest backups)", arg)
	}
	var dbs []string
	var match []inventory.Manifest
	for _, m := range held {
		if !slices.Contains(dbs, m.Database) {
			dbs = append(dbs, m.Database)
		}
		if db == "" || m.Database == db {
			match = append(match, m)
		}
	}
	switch {
	case len(held) == 0:
		return inventory.Manifest{}, failf("this PC holds no backups yet")
	case len(match) == 0:
		return inventory.Manifest{}, failf("this PC holds no backups of %s; it has: %s", db, strings.Join(slices.Sorted(slices.Values(dbs)), ", "))
	case db == "" && len(dbs) > 1:
		return inventory.Manifest{}, usagef("this PC holds backups of several databases (%s): add --db NAME", strings.Join(slices.Sorted(slices.Values(dbs)), ", "))
	}
	return slices.MaxFunc(match, func(a, b inventory.Manifest) int { return a.CreatedAt.Compare(b.CreatedAt) }), nil
}

// writeExport copies body into dir/<database>/<created>.sql.gz plus its
// manifest. The data goes through a temp file that is renamed only when size
// and sha256 match.
func writeExport(m inventory.Manifest, body io.Reader, dir string, force bool) (string, error) {
	switch fi, err := os.Stat(dir); {
	case errors.Is(err, fs.ErrNotExist):
		return "", failf("folder %s does not exist", dir)
	case err != nil:
		return "", failf("cannot open folder %s: %w", dir, err)
	case !fi.IsDir():
		return "", failf("%s is not a folder", dir)
	}
	dir = filepath.Join(dir, exportFolder(m.Database))
	_, err := os.Stat(dir)
	created := errors.Is(err, fs.ErrNotExist)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", failf("cannot create folder %s: %w", dir, err)
	}
	name := filepath.Join(dir, m.CreatedAt.Local().Format("2006-01-02_15-04-05"))
	data, manifest := name+inventory.DataExt, name+inventory.ManifestExt
	if !force {
		for _, p := range []string{data, manifest} {
			if _, err := os.Lstat(p); err == nil {
				return "", failf("%s already exists; add --force to replace it", p)
			}
		}
	}
	tmp, err := os.CreateTemp(dir, m.BackupID+"-*.part")
	if err != nil {
		return "", failf("cannot write in %s: %w", dir, err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(body, m.Size+1))
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil && (n != m.Size || hex.EncodeToString(h.Sum(nil)) != m.SHA256) {
		err = fmt.Errorf("the copy (%s) does not match the backup's sha256; try again, and if it repeats the backup on this PC is damaged",
			inventory.HumanBytes(n))
	}
	if err == nil {
		err = os.Rename(tmp.Name(), data)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
		if created {
			_ = os.Remove(dir)
		}
		return "", failf("export of %s stopped, nothing was written: %w", m.BackupID, err)
	}
	if err := inventory.WriteManifest(manifest, m); err != nil {
		return "", failf("the backup is at %s, but its manifest could not be written: %w", data, err)
	}
	return data, nil
}

func renderHeld(w io.Writer, held []inventory.Manifest) {
	if len(held) == 0 {
		fmt.Fprintln(w, "this PC holds no backups yet")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BACKUP\tDATABASE\tCREATED\tSIZE\tTAGS")
	for _, m := range held {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", m.BackupID, m.Database, m.CreatedAt.Local().Format("2006-01-02 15:04"),
			inventory.HumanBytes(m.Size), strings.Join(m.Tags, ","))
	}
	tw.Flush()
	fmt.Fprintln(w, "\ncopy one out: sevjala backups export <BACKUP> --to <FOLDER>")
}

// exportFolder makes a database name safe as a folder name on every OS.
func exportFolder(db string) string {
	if db == "." || db == ".." {
		return "_"
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, db)
}
