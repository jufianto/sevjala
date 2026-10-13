package dump

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jufianto/sevjala/inventory"
)

// ContentFlags are the blueprint's flags plus utf8mb4, so 4-byte characters
// survive an older client default (decisions D-021).
var ContentFlags = []string{"--routines", "--triggers", "--events", "--hex-blob", "--no-create-db",
	"--default-character-set=utf8mb4"}

type Options struct {
	ID          string
	Database    string
	StagingDir  string
	SourceAgent string
	Created     time.Time
	Tags        []string
	Tools       Tools
	Conn        Conn
	Progress    func(written int64) // compressed bytes so far; called from another goroutine
}

// Run streams mysqldump → gzip → (staging/<id>.part, sha256), renames the file
// into place and writes its manifest. Any failure deletes the .part.
func Run(ctx context.Context, o Options) (inventory.Manifest, error) {
	det, err := Detect(ctx, o.Tools, o.Conn)
	if err != nil {
		return inventory.Manifest{}, err
	}
	i := slices.IndexFunc(det.Databases, func(d Database) bool { return d.Name == o.Database })
	if i < 0 {
		return inventory.Manifest{}, fmt.Errorf("database %s does not exist on %s:%d", o.Database, o.Conn.Host, o.Conn.Port)
	}
	flags := append(slices.Clone(ContentFlags), det.Databases[i].Consistency())
	part := filepath.Join(o.StagingDir, o.ID+inventory.PartExt)
	data := filepath.Join(o.StagingDir, o.ID+inventory.DataExt)
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return inventory.Manifest{}, err
	}
	m, err := dumpTo(ctx, f, o, flags)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(part, data)
	}
	if err != nil {
		_ = os.Remove(part)
		return inventory.Manifest{}, err
	}
	m.ServerVersion = det.ServerVersion
	if err := inventory.WriteManifest(filepath.Join(o.StagingDir, o.ID+inventory.ManifestExt), m); err != nil {
		_ = os.Remove(data)
		return inventory.Manifest{}, err
	}
	return m, nil
}

func dumpTo(ctx context.Context, f *os.File, o Options, flags []string) (inventory.Manifest, error) {
	h := sha256.New()
	cw := &counter{w: io.MultiWriter(f, h), progress: o.Progress}
	gz := gzip.NewWriter(cw)
	cmd := exec.CommandContext(ctx, o.Tools.Dumper, append(append(o.Conn.args(), flags...), o.Database)...)
	cmd.Env = o.Conn.env()
	cmd.Stdout = gz
	stderr := &capped{max: 64 << 10}
	cmd.Stderr = stderr
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	msg := strings.TrimSpace(o.Conn.scrub(stderr.String()))
	if err == nil && strings.Contains(strings.ToLower(msg), "error") {
		err = errors.New("reported an error")
	}
	if err != nil {
		return inventory.Manifest{}, fmt.Errorf("%s %s: %w: %s", filepath.Base(o.Tools.Dumper), o.Database, err, msg)
	}
	if err := gz.Close(); err != nil {
		return inventory.Manifest{}, err
	}
	if err := f.Sync(); err != nil {
		return inventory.Manifest{}, err
	}
	return inventory.Manifest{V: 1, BackupID: o.ID, Database: o.Database, CreatedAt: o.Created.Truncate(time.Second),
		SourceAgent: o.SourceAgent, DumpFlags: flags, Size: cw.n, SHA256: hex.EncodeToString(h.Sum(nil)), Tags: o.Tags}, nil
}

type counter struct {
	w        io.Writer
	n        int64
	progress func(int64)
}

func (c *counter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if c.progress != nil {
		c.progress(c.n)
	}
	return n, err
}

// CleanStaging deletes dumps a previous run left unfinished.
func CleanStaging(dir string) ([]string, error) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, e := range es {
		if strings.HasSuffix(e.Name(), inventory.PartExt) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				return removed, err
			}
			removed = append(removed, e.Name())
		}
	}
	return removed, nil
}
