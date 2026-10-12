package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/config"
)

func TestPrepareDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")

	created, err := PrepareDataDir(dir, config.RoleNode)
	if err != nil || !created {
		t.Fatalf("first run: created=%v err=%v", created, err)
	}
	if _, err := os.Stat(config.LogDir(dir)); err != nil {
		t.Errorf("logs dir: %v", err)
	}
	cfg, err := config.Load(config.Path(dir))
	if err != nil || cfg.Role != config.RoleNode {
		t.Fatalf("written config = %+v, %v", cfg, err)
	}

	created, err = PrepareDataDir(dir, config.RoleNode)
	if err != nil || created {
		t.Errorf("same role again: created=%v err=%v", created, err)
	}

	before, _ := os.ReadFile(config.Path(dir))
	_, err = PrepareDataDir(dir, config.RoleController)
	if err == nil || !strings.Contains(err.Error(), `role "node"`) || !strings.Contains(err.Error(), `"controller"`) {
		t.Errorf("role change: err = %v", err)
	}
	after, _ := os.ReadFile(config.Path(dir))
	if string(before) != string(after) {
		t.Error("config rewritten on role mismatch")
	}
}

func TestPrepareDataDirBadConfig(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(config.Path(dir), []byte("role = \"x\""), 0o644)
	if _, err := PrepareDataDir(dir, config.RoleNode); err == nil {
		t.Error("invalid existing config accepted")
	}
}
