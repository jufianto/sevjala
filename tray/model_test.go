package tray

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
)

var now = time.Date(2026, 10, 17, 10, 27, 0, 0, time.Local)

func TestBuild(t *testing.T) {
	ctrl := &localapi.Status{Role: "controller", Hostname: "SERVER-01", Health: &localapi.Health{
		Level: localapi.HealthWarning, Headline: "PC-2 has not answered since 08:12 (2 h ago)",
		Items: []localapi.HealthItem{
			{Level: localapi.HealthWarning, Text: "PC-2 has not answered since 08:12 (2 h ago)"},
			{Level: localapi.HealthWarning, Text: "a"}, {Level: localapi.HealthWarning, Text: "b"},
			{Level: localapi.HealthWarning, Text: "c"}, {Level: localapi.HealthWarning, Text: "d"},
			{Level: localapi.HealthWarning, Text: "e"}, {Level: localapi.HealthWarning, Text: "f"}},
		Databases: []localapi.DBHealth{{Name: "shop", LastGood: "BK-2", LastGoodAt: now.Add(-7 * time.Minute), Copies: 1, Planned: 2},
			{Name: "legacy"}}}}
	node := &localapi.Status{Role: "node", Hostname: "PC-1", Store: &localapi.StoreInfo{Backups: 3, FreeBytes: 120 << 30},
		Health: &localapi.Health{Level: localapi.HealthOK, Headline: "Holding 3 backups; the controller last checked in at 10:26 (just now)", Items: []localapi.HealthItem{}}}
	tests := []struct {
		name   string
		st     *localapi.Status
		err    error
		color  string
		lines  []string
		backup bool
	}{
		{"controller", ctrl, nil, Yellow, []string{
			"PC-2 has not answered since 08:12 (2 h ago)",
			"shop: 10:20 (7 min ago), on 1 of 2 backup PCs",
			"legacy: no backup on a backup PC yet",
			"a", "b", "c", "d"}, true},
		{"node", node, nil, Green, []string{"Holding 3 backups; the controller last checked in at 10:26 (just now)", "120.0 GiB free"}, false},
		{"service down", nil, errors.New("dial: refused"), Grey, []string{"Sevjala service is not running"}, false},
		{"not allowed", nil, fmt.Errorf("open: %w", fs.ErrPermission), Grey, []string{"This account may not use Sevjala"}, false},
		{"problem", &localapi.Status{Role: "controller", Health: &localapi.Health{Level: localapi.HealthProblem, Headline: "x",
			Items: []localapi.HealthItem{{Level: localapi.HealthProblem, Text: "x"}}}}, nil, Red, []string{"x"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := Build(tt.st, tt.err, now)
			if m.Color != tt.color || !slices.Equal(m.Lines, tt.lines) || m.BackupNow != tt.backup || m.Tooltip != "Sevjala: "+tt.lines[0] {
				t.Errorf("got %+v", m)
			}
			if len(m.Lines) > MaxLines {
				t.Errorf("%d lines", len(m.Lines))
			}
		})
	}
}

func TestIcons(t *testing.T) {
	for _, c := range []string{Green, Yellow, Red, Grey} {
		img, err := png.Decode(bytes.NewReader(PNG(c)))
		if err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if _, _, _, a := img.At(16, 16).RGBA(); a == 0 {
			t.Errorf("%s: the centre is transparent", c)
		}
		if _, _, _, a := img.At(0, 0).RGBA(); a != 0 {
			t.Errorf("%s: the corner is not transparent", c)
		}
		ico := ICO(c)
		if binary.LittleEndian.Uint16(ico[2:]) != 1 || binary.LittleEndian.Uint16(ico[4:]) != 1 || ico[6] != 32 ||
			int(binary.LittleEndian.Uint32(ico[14:])) != len(ico)-22 || binary.LittleEndian.Uint32(ico[22:]) != 40 {
			t.Errorf("%s: bad ICO header % x", c, ico[:26])
		}
	}
	if bytes.Equal(PNG(Green), PNG(Red)) {
		t.Error("colours look the same")
	}
}

func TestBinaryReplaced(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "sevjala")
	if err := os.WriteFile(exe, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	w := watchBinary(exe)
	if w.replaced() {
		t.Error("replaced before anything changed")
	}
	if err := os.Rename(exe, exe+".old"); err != nil {
		t.Fatal(err)
	}
	if w.replaced() {
		t.Error("replaced while the new binary is not there yet")
	}
	if err := os.WriteFile(exe, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if !w.replaced() {
		t.Error("a new binary at the installed path was not seen")
	}
}

func TestQuitsWhenUninstalled(t *testing.T) {
	installed := true
	w := watchAutostart(func() bool { return installed })
	if w.removed() {
		t.Error("removed while installed")
	}
	installed = false
	if !w.removed() {
		t.Error("uninstall not seen")
	}
	if watchAutostart(func() bool { return false }).removed() {
		t.Error("a tray started without autostart (a dev run) must not quit")
	}
}

func TestOnlyAManagerOffersTheControllerDashboard(t *testing.T) {
	health := &localapi.Health{Level: localapi.HealthOK, Headline: "Holding 3 backups"}
	for _, tt := range []struct {
		name string
		st   *localapi.Status
		want bool
	}{
		{"manager", &localapi.Status{Role: "node", Health: health, Controller: &localapi.Peer{Hostname: "SERVER-01", Manager: true}}, true},
		{"node", &localapi.Status{Role: "node", Health: health, Controller: &localapi.Peer{Hostname: "SERVER-01"}}, false},
		{"unpaired", &localapi.Status{Role: "node", Health: health}, false},
	} {
		if m := Build(tt.st, nil, now); m.Manage != tt.want {
			t.Errorf("%s: Manage %v", tt.name, m.Manage)
		}
	}
}
