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
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/svc"
)

func newBackupsExportCmd(g *globals) *cobra.Command {
	var db, to string
	var force bool
	c := &cobra.Command{
		Use:   "export <backup|latest|all>",
		Short: "Copy backups this PC holds into a folder you can open",
		Long: `Copies backups out of Sevjala's locked folder into <FOLDER>/<database>/, each named by
when it was made: 2026-10-14_00-20-00.sql.gz (the dump) and 2026-10-14_00-20-00.json (what
it is and its sha256). Each copy is checked against its sha256 while it is written, and
belongs to you, not to the service. Sevjala's own copy stays where it is.

Works on a backup PC (what 'sevjala backups list' shows there) and on the controller
(only the newest backups, which it keeps in staging). An existing file is never replaced
unless you add --force; with all, backups already there are skipped, so running it again
copies only the new ones. It asks for administrator rights: the copy carries the whole database.`,
		Example: `  sevjala backups export latest --to ~/Desktop
  sevjala backups export latest --db shop --to D:\restore
  sevjala backups export all --to D:\backups              (every backup of every database)
  sevjala backups export BK-20261014-002000-30D3          (into the current folder)`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if !svc.IsAdmin() {
				return &needsAdmin{why: "exporting a backup needs administrator rights"}
			}
			held, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]inventory.Manifest, error) {
				return cl.Held(ctx)
			})
			if err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			cl := localapi.NewClient(localapi.Endpoint(dataDir))
			if args[0] == "all" {
				open := func(ctx context.Context, id string) (io.ReadCloser, error) {
					rc, err := cl.OpenHeld(ctx, id)
					if err != nil {
						return nil, agentProblem(err, nil)
					}
					return rc, nil
				}
				return exportAll(ctx, held, db, open, to, force, c.OutOrStdout())
			}
			m, err := pickHeld(held, args[0], db)
			if err != nil {
				return err
			}
			body, err := cl.OpenHeld(ctx, m.BackupID)
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
	c.Flags().StringVar(&db, "db", "", "with latest: the newest backup of database `NAME`; with all: only that database")
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
	if err := checkFolder(dir); err != nil {
		return "", err
	}
	name := exportName(dir, m)
	dir = filepath.Dir(name)
	_, err := os.Stat(dir)
	created := errors.Is(err, fs.ErrNotExist)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", failf("cannot create folder %s: %w", dir, err)
	}
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
	if uid, gid, ok := sudoOwner(); ok {
		mine := []string{data, manifest}
		if created {
			mine = append(mine, dir)
		}
		for _, p := range mine {
			_ = lchown(p, uid, gid)
		}
	}
	return data, nil
}

func renderHeld(w io.Writer, held []inventory.Manifest, sources []inventory.Source) {
	if len(held) == 0 {
		fmt.Fprintln(w, "this PC holds no backups yet")
		return
	}
	from := map[string]string{}
	for _, s := range sources {
		from[s.AgentID] = sourceName(s)
		if !s.Current {
			from[s.AgentID] += " (former)"
		}
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BACKUP\tDATABASE\tFROM\tCREATED\tSIZE\tTAGS")
	for _, m := range held {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", m.BackupID, m.Database, from[m.SourceAgent], m.CreatedAt.Local().Format("2006-01-02 15:04"),
			inventory.HumanBytes(m.Size), strings.Join(m.Tags, ","))
	}
	tw.Flush()
	fmt.Fprintln(w, "\ncopy one out: sevjala backups export <BACKUP> --to <FOLDER>")
}

// sourceName is a controller's PC name, or the start of its agent id before
// its first named backup.
func sourceName(s inventory.Source) string {
	if s.Hostname != "" {
		return s.Hostname
	}
	return shortID(s.AgentID)
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

func checkFolder(dir string) error {
	switch fi, err := os.Stat(dir); {
	case errors.Is(err, fs.ErrNotExist):
		return failf("folder %s does not exist", dir)
	case err != nil:
		return failf("cannot open folder %s: %w", dir, err)
	case !fi.IsDir():
		return failf("%s is not a folder", dir)
	}
	return nil
}

// exportName is where a backup lands under dir, without its extension.
func exportName(dir string, m inventory.Manifest) string {
	return filepath.Join(dir, exportFolder(m.Database), m.CreatedAt.Local().Format("2006-01-02_15-04-05"))
}

// exportAll copies every held backup (of db, when given) into dir, skipping
// those already there unless force; one failure does not stop the rest.
func exportAll(ctx context.Context, held []inventory.Manifest, db string, open func(context.Context, string) (io.ReadCloser, error),
	dir string, force bool, w io.Writer) error {
	var todo []inventory.Manifest
	for _, m := range held {
		if db == "" || m.Database == db {
			todo = append(todo, m)
		}
	}
	switch {
	case len(todo) == 0 && db != "":
		return failf("this PC holds no backups of %s", db)
	case len(todo) == 0:
		return failf("this PC holds no backups yet")
	}
	if err := checkFolder(dir); err != nil {
		return err
	}
	exported, skipped, failed := 0, 0, 0
	for _, m := range todo {
		name := exportName(dir, m)
		rel, _ := filepath.Rel(dir, name+inventory.DataExt)
		if _, err := os.Lstat(name + inventory.DataExt); err == nil && !force {
			skipped++
			continue
		}
		body, err := open(ctx, m.BackupID)
		if err == nil {
			_, err = writeExport(m, body, dir, force)
			body.Close()
		}
		if err != nil {
			failed++
			fmt.Fprintf(w, "%s: failed: %v\n", rel, err)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		exported++
		fmt.Fprintln(w, rel)
	}
	fmt.Fprintf(w, "exported %d, skipped %d already there", exported, skipped)
	if failed > 0 {
		fmt.Fprintf(w, ", %d failed\n", failed)
		return errSilent1
	}
	fmt.Fprintln(w)
	return nil
}

var lchown = os.Lchown

// sudoOwner is the user who ran sudo: an export belongs to them, not to root.
func sudoOwner() (uid, gid int, ok bool) {
	uid, err1 := strconv.Atoi(os.Getenv("SUDO_UID"))
	gid, err2 := strconv.Atoi(os.Getenv("SUDO_GID"))
	return uid, gid, err1 == nil && err2 == nil
}
