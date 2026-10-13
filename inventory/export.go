package inventory

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// List returns the complete backups in dir (a node's store or the
// controller's staging), oldest first. Unlike Scan it changes nothing.
func List(dir string) ([]Manifest, error) {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var ms []Manifest
	for _, e := range es {
		id, ok := strings.CutSuffix(e.Name(), ManifestExt)
		if !ok || !ValidID(id) {
			continue
		}
		m, err := ReadManifest(filepath.Join(dir, e.Name()))
		if err != nil || m.BackupID != id {
			continue
		}
		if fi, err := os.Stat(filepath.Join(dir, id+DataExt)); err == nil && fi.Size() == m.Size {
			ms = append(ms, m)
		}
	}
	slices.SortFunc(ms, func(a, b Manifest) int { return strings.Compare(a.BackupID, b.BackupID) })
	return ms, nil
}

// OpenBackup opens a complete backup in dir for reading.
func OpenBackup(dir, id string) (Manifest, *os.File, error) {
	if !ValidID(id) {
		return Manifest{}, nil, fmt.Errorf("%w: backup id %q is not BK-yyyymmdd-hhmmss-XXXX", ErrInvalid, id)
	}
	m, err := ReadManifest(filepath.Join(dir, id+ManifestExt))
	if err != nil {
		return Manifest{}, nil, err
	}
	f, err := os.Open(filepath.Join(dir, id+DataExt))
	if err != nil {
		return Manifest{}, nil, err
	}
	if fi, err := f.Stat(); err != nil || fi.Size() != m.Size {
		f.Close()
		return Manifest{}, nil, fmt.Errorf("%w: %s is not the size its manifest says", ErrMismatch, id)
	}
	return m, f, nil
}

// Open is OpenBackup on the store; until the reader is closed, Delete and
// Receive of that backup return ErrBusy.
func (s *Store) Open(id string) (Manifest, io.ReadCloser, error) {
	if !s.claim(id) {
		return Manifest{}, nil, fmt.Errorf("%w: %s is being received or deleted", ErrBusy, id)
	}
	m, f, err := OpenBackup(s.dir, id)
	if err != nil {
		s.release(id)
		return Manifest{}, nil, err
	}
	return m, &held{File: f, release: func() { s.release(id) }}, nil
}

type held struct {
	*os.File
	release func()
}

func (h *held) Close() error {
	err := h.File.Close()
	h.release()
	return err
}
