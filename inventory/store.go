package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jufianto/sevjala/logx"
)

const (
	StateVerified = "verified"
	StateCorrupt  = "corrupt"
	StalePart     = time.Hour
)

var (
	ErrInvalid  = errors.New("invalid backup")
	ErrExists   = errors.New("already stored")
	ErrConflict = errors.New("conflict")
	ErrMismatch = errors.New("hash or size mismatch")
	ErrNoSpace  = errors.New("not enough free space")
)

type Copy struct {
	BackupID string
	Size     int64
	SHA256   string
	State    string
}

type Inventory struct {
	Backups  []Copy
	Problems []string
}

type Store struct {
	dir     string
	minFree int64
	log     *slog.Logger
	now     func() time.Time
	free    func(string) (int64, error)

	mu       sync.Mutex
	inFlight map[string]bool
	removed  []string // reported by the next Scan
}

// Open creates the store dir and deletes what a previous run left half
// written; no transfer survives an agent restart.
func Open(dir string, minFree int64, log *slog.Logger) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, minFree: minFree, log: log, now: time.Now, free: FreeBytes, inFlight: map[string]bool{}}
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range es {
		if strings.HasSuffix(e.Name(), PartExt) || strings.HasSuffix(e.Name(), ManifestExt+".tmp") {
			s.remove(e.Name(), "left by a previous run")
		}
	}
	return s, nil
}

func (s *Store) Dir() string { return s.dir }

func (s *Store) FreeBytes() (int64, error) { return s.free(s.dir) }

func (s *Store) path(id, ext string) string { return filepath.Join(s.dir, id+ext) }

func (s *Store) claim(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inFlight[id] {
		return false
	}
	s.inFlight[id] = true
	return true
}

func (s *Store) release(id string) {
	s.mu.Lock()
	delete(s.inFlight, id)
	s.mu.Unlock()
}

func (s *Store) busy(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.inFlight[id]
}

func (s *Store) remove(name, why string) {
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.log.Error("remove partial transfer", "file", name, "err", err)
		return
	}
	msg := fmt.Sprintf("removed partial transfer %s (%s)", name, why)
	s.log.Warn(msg, logx.Event(logx.EventPartRemoved), "file", name, "reason", why)
	s.mu.Lock()
	s.removed = append(s.removed, msg)
	s.mu.Unlock()
}

// Receive writes body to <id>.part while hashing it, and keeps it only when
// size and sha256 match: then the rename makes it <id>.sql.gz and the
// manifest is written next to it. Anything else deletes the .part.
func (s *Store) Receive(m Manifest, sha string, size int64, body io.Reader) error {
	if err := m.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if m.SHA256 != sha || m.Size != size {
		return fmt.Errorf("%w: the manifest does not match the sha256 and size headers", ErrInvalid)
	}
	id := m.BackupID
	if !s.claim(id) {
		return fmt.Errorf("%w: %s is already being received", ErrConflict, id)
	}
	defer s.release(id)
	if old, err := ReadManifest(s.path(id, ManifestExt)); err == nil {
		if old.SHA256 != m.SHA256 {
			return fmt.Errorf("%w: %s is stored with sha256 %s", ErrConflict, id, old.SHA256)
		}
		if fi, err := os.Stat(s.path(id, DataExt)); err == nil && fi.Size() == old.Size {
			return ErrExists
		}
	}
	if free, err := s.free(s.dir); err == nil && free-size < s.minFree {
		return fmt.Errorf("%w: %d bytes free, the backup needs %d and %d must stay free", ErrNoSpace, free, size, s.minFree)
	}
	part := s.path(id, PartExt)
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, size+1))
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if got := hex.EncodeToString(h.Sum(nil)); err == nil && (n != size || got != sha) {
		err = fmt.Errorf("%w: received %d bytes with sha256 %s, expected %d bytes with sha256 %s", ErrMismatch, n, got, size, sha)
	}
	if err == nil {
		err = os.Rename(part, s.path(id, DataExt))
	}
	if err != nil {
		_ = os.Remove(part)
		return err
	}
	return WriteManifest(s.path(id, ManifestExt), m)
}

// Scan lists the store without re-hashing (that happens before restore) and
// removes .part files older than StalePart that no transfer holds.
func (s *Store) Scan() (Inventory, error) {
	es, err := os.ReadDir(s.dir)
	if err != nil {
		return Inventory{}, err
	}
	data := map[string]int64{}
	manifests := map[string]bool{}
	for _, e := range es {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, PartExt):
			if s.busy(strings.TrimSuffix(name, PartExt)) {
				continue
			}
			if fi, err := e.Info(); err == nil && s.now().Sub(fi.ModTime()) > StalePart {
				s.remove(name, "older than 1h")
			}
		case strings.HasSuffix(name, DataExt) && ValidID(strings.TrimSuffix(name, DataExt)):
			if fi, err := e.Info(); err == nil {
				data[strings.TrimSuffix(name, DataExt)] = fi.Size()
			}
		case strings.HasSuffix(name, ManifestExt) && ValidID(strings.TrimSuffix(name, ManifestExt)):
			manifests[strings.TrimSuffix(name, ManifestExt)] = true
		}
	}
	ids := make([]string, 0, len(data)+len(manifests))
	for id := range data {
		ids = append(ids, id)
	}
	for id := range manifests {
		if _, ok := data[id]; !ok {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	var inv Inventory
	for _, id := range ids {
		size, hasData := data[id]
		c := Copy{BackupID: id, Size: size, State: StateCorrupt}
		var problem string
		if !manifests[id] {
			problem = id + ": backup file without its manifest"
		} else if m, err := ReadManifest(s.path(id, ManifestExt)); err != nil {
			problem = fmt.Sprintf("%s: unreadable manifest: %v", id, err)
		} else {
			c.Size, c.SHA256 = m.Size, m.SHA256
			switch {
			case !hasData:
				problem = id + ": manifest without its backup file"
			case size != m.Size:
				problem = fmt.Sprintf("%s: file is %d bytes, the manifest says %d", id, size, m.Size)
			default:
				c.State = StateVerified
			}
		}
		inv.Backups = append(inv.Backups, c)
		if problem != "" {
			inv.Problems = append(inv.Problems, problem)
		}
	}
	s.mu.Lock()
	inv.Problems = append(inv.Problems, s.removed...)
	s.removed = nil
	s.mu.Unlock()
	return inv, nil
}
