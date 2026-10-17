// Package inventory is a node's backup store: the directory of backup files
// and sidecar manifests that is the truth for "actual" (blueprint AD-07).
package inventory

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"
)

const (
	DataExt     = ".sql.gz"
	ManifestExt = ".json"
	PartExt     = ".part"
)

type Manifest struct {
	V             int       `json:"v"`
	BackupID      string    `json:"backup_id"`
	Database      string    `json:"database"`
	CreatedAt     time.Time `json:"created_at"`
	SourceAgent   string    `json:"source_agent"`
	ServerVersion string    `json:"server_version"`
	DumpFlags     []string  `json:"dump_flags"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	Tags          []string  `json:"tags"`
}

var (
	idPattern  = regexp.MustCompile(`^BK-\d{8}-\d{6}-[0-9A-F]{4}$`)
	shaPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ValidID also keeps paths out of a backup ID: it is used as a file name.
func ValidID(id string) bool { return idPattern.MatchString(id) }

func NewID(t time.Time) string {
	var b [2]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("BK-%s-%04X", t.Format("20060102-150405"), binary.BigEndian.Uint16(b[:]))
}

func (m Manifest) Validate() error {
	switch {
	case m.V != 1:
		return fmt.Errorf("manifest version %d: want 1", m.V)
	case !ValidID(m.BackupID):
		return fmt.Errorf("backup id %q is not BK-yyyymmdd-hhmmss-XXXX", m.BackupID)
	case !shaPattern.MatchString(m.SHA256):
		return fmt.Errorf("sha256 %q is not 64 lowercase hex", m.SHA256)
	case m.Size <= 0:
		return fmt.Errorf("size %d", m.Size)
	case m.Database == "":
		return errors.New("database is empty")
	}
	return nil
}

func ReadManifest(path string) (Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	if err := m.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

func WriteManifest(path string, m Manifest) error {
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return WriteFile(path, append(raw, '\n'), 0o600)
}

// WriteFile goes through a synced temp file, a rename and a directory sync,
// so a power cut leaves the old file or the complete new one.
func WriteFile(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), "*-"+filepath.Base(path)+".tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = f.Chmod(mode)
	if err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return SyncDir(filepath.Dir(path))
}

// SyncDir makes renames in dir durable; Windows has no directory fsync.
func SyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
