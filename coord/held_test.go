package coord

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
)

const heldID = "BK-20261014-002000-30D3"

func heldManifest(data []byte) inventory.Manifest {
	sum := sha256.Sum256(data)
	return inventory.Manifest{V: 1, BackupID: heldID, Database: "shop", CreatedAt: time.Date(2026, 10, 14, 0, 20, 0, 0, time.UTC),
		SourceAgent: "c960acf5", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Tags: []string{"manual"}}
}

func TestHeldOnBothRoles(t *testing.T) {
	data := []byte("-- dump\n")
	m := heldManifest(data)
	inv, err := inventory.Open(t.TempDir(), 0, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := inv.Receive(m, m.SHA256, m.Size, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	staging := t.TempDir()
	if err := os.WriteFile(filepath.Join(staging, heldID+inventory.DataExt), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := inventory.WriteManifest(filepath.Join(staging, heldID+inventory.ManifestExt), m); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for name, s := range map[string]*Service{
		"node":       {Node: &Node{Store: inv}},
		"controller": {Controller: &Controller{Staging: staging}},
	} {
		ms, err := s.Held(ctx)
		if err != nil || len(ms) != 1 || ms[0].BackupID != heldID {
			t.Errorf("%s: Held %+v, %v", name, ms, err)
		}
		_, rc, err := s.OpenHeld(asAdmin, heldID)
		if err != nil {
			t.Fatalf("%s: OpenHeld: %v", name, err)
		}
		got, _ := io.ReadAll(rc)
		rc.Close()
		if !bytes.Equal(got, data) {
			t.Errorf("%s: read %q", name, got)
		}
		for _, tt := range []struct {
			id   string
			code int
		}{{"../x", http.StatusBadRequest}, {"BK-20261014-002000-FFFF", http.StatusNotFound}} {
			var e *localapi.Error
			if _, _, err := s.OpenHeld(asAdmin, tt.id); !errors.As(err, &e) || e.Code != tt.code || !strings.Contains(e.Msg, tt.id) {
				t.Errorf("%s %s: err = %#v", name, tt.id, err)
			}
		}
	}
}

func TestOpenHeldWhileDeletingIsBusy(t *testing.T) {
	data := []byte("-- dump\n")
	m := heldManifest(data)
	inv, err := inventory.Open(t.TempDir(), 0, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if err := inv.Receive(m, m.SHA256, m.Size, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	s := &Service{Node: &Node{Store: inv}}
	_, rc, err := s.OpenHeld(asAdmin, heldID)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	var e *localapi.Error
	if _, _, err := s.OpenHeld(asAdmin, heldID); !errors.As(err, &e) || e.Code != http.StatusConflict {
		t.Errorf("second open: %#v", err)
	}
}
