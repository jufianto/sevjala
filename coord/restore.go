package coord

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/dump"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/store"
)

const ownCopy = "the controller's own copy"

// errDamaged marks a copy whose size or sha256 differs from the catalog's.
var errDamaged = errors.New("damaged: its size or sha256 does not match the backup")

type restoreRun struct {
	mu   sync.Mutex
	view localapi.Restore
}

func (r *restoreRun) get() localapi.Restore {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.view
}

func (r *restoreRun) set(f func(v *localapi.Restore)) {
	r.mu.Lock()
	f(&r.view)
	r.mu.Unlock()
}

func (r *restoreRun) step(source, step string, size int64) {
	r.set(func(v *localapi.Restore) {
		if source != "" {
			v.Source = source
		}
		v.Step, v.Bytes, v.Size = step, 0, size
	})
}

// Restore checks the request, then restores in the background on the agent's
// lifetime (blueprint: Backup engine → Restore); RestoreStatus follows it.
func (c *Controller) Restore(ctx context.Context, req localapi.RestoreRequest) (localapi.Restore, error) {
	cfg := c.config()
	b, err := c.restoreBackup(ctx, req, cfg)
	if err != nil {
		return localapi.Restore{}, err
	}
	tools, conn, err := c.mysql(cfg)
	if err != nil {
		return localapi.Restore{}, err
	}
	target := cmp.Or(req.Into, dump.DefaultTarget(b.Database, b.CreatedAt))
	if !dump.ValidName(target) {
		return localapi.Restore{}, localapi.Errorf(http.StatusBadRequest,
			"%q cannot be a database name here: use letters, digits, _, $ or -, at most 64", target)
	}
	if _, _, exists, err := dump.Schema(ctx, tools, conn, target); err != nil {
		return localapi.Restore{}, err
	} else if exists {
		return localapi.Restore{}, localapi.Errorf(http.StatusConflict, "database %s already exists; choose another name with --into", target)
	}
	run := &restoreRun{view: localapi.Restore{BackupID: b.BackupID, Database: b.Database, Target: target,
		State: localapi.RestoreRunning, Step: "starting", Size: b.Size, StartedAt: time.Now()}}
	if err := c.claimRestore(run); err != nil {
		return localapi.Restore{}, err
	}
	go c.runRestore(run, b, tools, conn)
	return run.get(), nil
}

func (c *Controller) RestoreStatus(context.Context) (localapi.Restore, error) {
	c.mu.Lock()
	run := c.restore
	c.mu.Unlock()
	if run == nil {
		return localapi.Restore{}, localapi.Errorf(http.StatusNotFound, "no restore has run since the service started")
	}
	return run.get(), nil
}

func (c *Controller) claimRestore(run *restoreRun) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.restore != nil {
		if v := c.restore.get(); v.State == localapi.RestoreRunning {
			return localapi.Errorf(http.StatusConflict, "a restore is already running: %s into %s", v.BackupID, v.Target)
		}
	}
	c.restore = run
	return nil
}

// restoringID is the backup a running restore reads, which retention must
// not unstage; "" when none runs. Called with c.mu held.
func (c *Controller) restoringID() string {
	if c.restore == nil {
		return ""
	}
	if v := c.restore.get(); v.State == localapi.RestoreRunning {
		return v.BackupID
	}
	return ""
}

// restoreBackup finds the backup a request names: an id, or the newest
// backup of a database that still has a copy to read.
func (c *Controller) restoreBackup(ctx context.Context, req localapi.RestoreRequest, cfg config.Config) (store.Backup, error) {
	if req.Backup != "" && req.Backup != "latest" {
		b, err := c.Store.Backup(ctx, req.Backup)
		switch {
		case errors.Is(err, store.ErrNotFound):
			return b, localapi.Errorf(http.StatusNotFound, "no backup %s; 'sevjala backups list' shows them", req.Backup)
		case err != nil:
			return b, err
		case b.State == store.BackupExpired:
			return b, localapi.Errorf(http.StatusConflict, "%s has expired: no copy of it is left anywhere", b.BackupID)
		}
		return b, nil
	}
	db := req.DB
	if db == "" {
		switch dbs := cfg.Controller.Database.Databases; len(dbs) {
		case 1:
			db = dbs[0]
		case 0:
			return store.Backup{}, localapi.Errorf(http.StatusBadRequest, "no database is configured; name one with --db NAME")
		default:
			return store.Backup{}, localapi.Errorf(http.StatusBadRequest, "several databases are backed up (%s): add --db NAME",
				strings.Join(dbs, ", "))
		}
	}
	bs, err := c.Store.Backups(ctx, false)
	if err != nil {
		return store.Backup{}, err
	}
	for _, b := range bs { // newest first
		if b.Database == db && (b.StagingPath != "" || slices.ContainsFunc(b.Copies, func(cp store.Copy) bool {
			return cp.State == store.CopyVerified
		})) {
			return b, nil
		}
	}
	return store.Backup{}, localapi.Errorf(http.StatusNotFound, "no backup of %s to restore", db)
}

// mysql is the configured client and connection; the password comes from the keyring.
func (c *Controller) mysql(cfg config.Config) (dump.Tools, dump.Conn, error) {
	db := cfg.Controller.Database
	pw, err := config.Secret(c.DataDir, db.PasswordRef)
	if err != nil {
		return dump.Tools{}, dump.Conn{}, fmt.Errorf("database password: %w", err)
	}
	tools, err := c.FindTools(db.Dumper)
	if err != nil {
		return dump.Tools{}, dump.Conn{}, err
	}
	return tools, dump.Conn{Host: db.Host, Port: db.Port, User: db.User, Password: pw}, nil
}

func (c *Controller) runRestore(run *restoreRun, b store.Backup, tools dump.Tools, conn dump.Conn) {
	v := run.get()
	log := c.Log.With("backup_id", b.BackupID, "database", b.Database, "target", v.Target)
	log.Info("restore started", logx.Event(logx.EventRestoreStarted), "replace", v.Replace)
	err := c.restoreInto(run, b, tools, conn, log)
	run.set(func(v *localapi.Restore) {
		v.FinishedAt = time.Now()
		v.State = localapi.RestoreDone
		if err != nil {
			v.State, v.Error = localapi.RestoreFailed, err.Error()
		}
	})
	v = run.get()
	if err != nil {
		log.Error("restore failed", logx.Event(logx.EventRestoreFailed), "step", v.Step, "err", err)
		return
	}
	log.Info("restore succeeded", logx.Event(logx.EventRestoreSucceeded), "source", v.Source,
		"duration", v.FinishedAt.Sub(v.StartedAt).Round(time.Second).String(), "tables", v.Tables)
}

// restoreInto loads b into a new database, which it removes again when loading fails.
func (c *Controller) restoreInto(run *restoreRun, b store.Backup, tools dump.Tools, conn dump.Conn, log *slog.Logger) error {
	path, done, err := c.fetchVerified(run, b, log)
	if err != nil {
		return err
	}
	defer done()
	target := run.get().Target
	var charset, collation string
	if dump.ValidName(b.Database) {
		if charset, collation, _, err = dump.Schema(c.Ctx, tools, conn, b.Database); err != nil {
			return err
		}
	}
	run.step("", "creating database "+target, 0)
	if err := dump.CreateDatabase(c.Ctx, tools, conn, target, charset, collation); err != nil {
		return err
	}
	if err := c.load(run, tools, conn, target, path, b.Size); err != nil {
		if derr := dump.DropDatabase(context.WithoutCancel(c.Ctx), tools, conn, target); derr != nil {
			return fmt.Errorf("%w; the half-made database %s could not be removed: %w", err, target, derr)
		}
		return fmt.Errorf("%w; the half-made database %s was removed", err, target)
	}
	return c.countTables(run, tools, conn, target)
}

func (c *Controller) load(run *restoreRun, tools dump.Tools, conn dump.Conn, db, path string, size int64) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	run.step("", "loading into "+db, size)
	return dump.Load(c.Ctx, tools, conn, db, &progress{r: f, run: run})
}

func (c *Controller) countTables(run *restoreRun, tools dump.Tools, conn dump.Conn, db string) error {
	n, err := dump.TableCount(c.Ctx, tools, conn, db)
	if err != nil {
		return err
	}
	run.set(func(v *localapi.Restore) { v.Tables = n })
	return nil
}

// fetchVerified returns a file holding b with the catalog's size and sha256:
// the controller's own copy, else a download from each online node with a
// verified copy, newest first. done removes a downloaded file.
func (c *Controller) fetchVerified(run *restoreRun, b store.Backup, log *slog.Logger) (string, func(), error) {
	var tried []string
	fail := func(source string, err error) {
		tried = append(tried, source+": "+err.Error())
		log.Warn("restore source failed", logx.Event(logx.EventRestoreSourceFail), "source", source, "err", err)
	}
	if b.StagingPath != "" {
		path := filepath.Join(c.Staging, b.BackupID+inventory.DataExt)
		run.step(ownCopy, "checking "+ownCopy, b.Size)
		err := verifyFile(path, b, run)
		if err == nil {
			return path, func() {}, nil
		}
		fail(ownCopy, err)
		if errors.Is(err, errDamaged) {
			c.dropOwnCopy(b.BackupID, log)
		}
	}
	nodes, err := c.restoreNodes(b)
	if err != nil {
		return "", nil, err
	}
	for _, n := range nodes {
		run.step(n.Hostname, "fetching from "+n.Hostname, b.Size)
		path, err := c.download(run, b, n)
		if err == nil {
			return path, func() { _ = os.Remove(path) }, nil
		}
		fail(n.Hostname, err)
		if errors.Is(err, errDamaged) {
			if err := c.Store.MarkCopy(c.Ctx, b.BackupID, n.AgentID, store.CopyCorrupt, "damaged: found by a restore", time.Now()); err != nil {
				log.Error("restore: mark copy corrupt", "agent_id", n.AgentID, "err", err)
			}
			log.Warn("copy corrupt", logx.Event(logx.EventCopyCorrupt), "agent_id", n.AgentID, "host", n.Hostname)
		}
	}
	if len(tried) == 0 {
		return "", nil, fmt.Errorf("no copy of %s can be read: %s is gone and no backup PC holding it is online", b.BackupID, ownCopy)
	}
	return "", nil, fmt.Errorf("no good copy of %s could be read: %s", b.BackupID, strings.Join(tried, "; "))
}

// restoreNodes are the online nodes holding a verified copy of b, newest copy first.
func (c *Controller) restoreNodes(b store.Backup) ([]store.Agent, error) {
	copies := slices.Clone(b.Copies)
	slices.SortFunc(copies, func(x, y store.Copy) int { return y.VerifiedAt.Compare(x.VerifiedAt) })
	var out []store.Agent
	for _, cp := range copies {
		if cp.State != store.CopyVerified {
			continue
		}
		a, err := c.Store.Agent(c.Ctx, cp.AgentID)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if a.State == store.AgentOnline {
			out = append(out, a)
		}
	}
	return out, nil
}

// download writes n's copy of b to staging/<id>.restore.part while hashing it.
func (c *Controller) download(run *restoreRun, b store.Backup, n store.Agent) (string, error) {
	_, body, err := c.LAN.GetBackup(c.Ctx, net.JoinHostPort(n.LastIP, strconv.Itoa(n.Port)), n.FP, b.BackupID)
	if err != nil {
		return "", err
	}
	defer body.Close()
	path := filepath.Join(c.Staging, b.BackupID+".restore"+inventory.PartExt)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	written, err := io.Copy(io.MultiWriter(f, h), &progress{r: io.LimitReader(body, b.Size+1), run: run})
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && (written != b.Size || hex.EncodeToString(h.Sum(nil)) != b.SHA256) {
		err = errDamaged
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func verifyFile(path string, b store.Backup, run *restoreRun) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, &progress{r: f, run: run})
	if err != nil {
		return err
	}
	if n != b.Size || hex.EncodeToString(h.Sum(nil)) != b.SHA256 {
		return errDamaged
	}
	return nil
}

// dropOwnCopy deletes a damaged staging copy so backfill never sends it.
func (c *Controller) dropOwnCopy(id string, log *slog.Logger) {
	for _, ext := range []string{inventory.DataExt, inventory.ManifestExt} {
		if err := os.Remove(filepath.Join(c.Staging, id+ext)); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Error("restore: remove damaged copy", "file", id+ext, "err", err)
		}
	}
	if err := c.Store.Unstage(c.Ctx, id); err != nil {
		log.Error("restore: unstage damaged copy", "err", err)
	}
	log.Warn("damaged copy removed from the controller", logx.Event(logx.EventCopyCorrupt), "agent_id", c.Self.AgentID)
}

// progress adds what is read to the restore's Bytes.
type progress struct {
	r   io.Reader
	run *restoreRun
}

func (p *progress) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.run.set(func(v *localapi.Restore) { v.Bytes += int64(n) })
	return n, err
}
