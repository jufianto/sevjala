package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	for _, tt := range []struct {
		a, b string
		want int
	}{
		{"0.3.0", "0.3.0", 0},
		{"v0.3.0", "0.3.0+16", 0},
		{"0.3.0-alpha.1+16", "0.3.0-alpha.1", 0},
		{"0.3.1", "0.3.0", 1},
		{"0.10.0", "0.9.9", 1},
		{"0.3.0", "0.3.0-alpha.1", 1},
		{"0.3.0-alpha.2", "0.3.0-alpha.1", 1},
		{"0.3.0-alpha.10", "0.3.0-alpha.9", 1},
		{"0.3.0-beta", "0.3.0-alpha.1", 1},
		{"0.3.0-alpha.1", "0.3.0-alpha", 1},
		{"0.3.0-alpha.1", "0.3.0-alpha.beta", -1},
	} {
		a, aok := parseVersion(tt.a)
		b, bok := parseVersion(tt.b)
		if !aok || !bok {
			t.Fatalf("parse %q %q", tt.a, tt.b)
		}
		if got := compareVersions(a, b); got != tt.want {
			t.Errorf("%s vs %s = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
	for _, s := range []string{"dev", "", "1.2", "1.2.x", "1.2.3.4"} {
		if _, ok := parseVersion(s); ok {
			t.Errorf("%q parsed", s)
		}
	}
}

func TestLatestRelease(t *testing.T) {
	rs := []release{
		{Tag: "v0.4.0-alpha.1", Prerelease: true},
		{Tag: "v0.5.0", Draft: true},
		{Tag: "v0.3.0"},
		{Tag: "v0.2.0"},
		{Tag: "nightly"},
	}
	for current, want := range map[string]string{
		"0.2.0":            "v0.3.0",
		"0.3.0-alpha.1+16": "v0.4.0-alpha.1",
		"dev":              "v0.4.0-alpha.1",
	} {
		if r, ok := latestRelease(rs, current); !ok || r.Tag != want {
			t.Errorf("from %s: got %s, want %s", current, r.Tag, want)
		}
	}
	if _, ok := latestRelease([]release{{Tag: "v1.0.0", Draft: true}}, "0.1.0"); ok {
		t.Error("a draft is never offered")
	}
}

func TestAssetName(t *testing.T) {
	for _, tt := range []struct{ goos, goarch, want string }{
		{"windows", "amd64", "sevjala-win7-amd64.exe"},
		{"windows", "386", "sevjala-win7-386.exe"},
		{"darwin", "arm64", "sevjala-darwin-arm64"},
		{"linux", "amd64", "sevjala-linux-amd64"},
	} {
		if got := assetName(tt.goos, tt.goarch); got != tt.want {
			t.Errorf("%s/%s: %s", tt.goos, tt.goarch, got)
		}
	}
}

// fakeGitHub serves one release with a darwin/arm64 asset; sum overrides the
// checksum it publishes when not empty.
func fakeGitHub(t *testing.T, tag, sum string) (api string, body []byte) {
	t.Helper()
	body = []byte("new sevjala binary")
	if sum == "" {
		h := sha256.Sum256(body)
		sum = hex.EncodeToString(h[:])
	}
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/releases", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"tag_name": tag, "assets": []map[string]string{
			{"name": "sevjala-darwin-arm64", "browser_download_url": srv.URL + "/dl/sevjala-darwin-arm64"},
			{"name": "checksums.txt", "browser_download_url": srv.URL + "/dl/checksums.txt"},
		}}})
	})
	mux.HandleFunc("/dl/sevjala-darwin-arm64", func(w http.ResponseWriter, _ *http.Request) { w.Write(body) })
	mux.HandleFunc("/dl/checksums.txt", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "%s  sevjala-linux-amd64\n%s  sevjala-darwin-arm64\n", strings.Repeat("0", 64), sum)
	})
	return srv.URL + "/releases", body
}

func TestFetchUpdate(t *testing.T) {
	ctx := context.Background()
	api, body := fakeGitHub(t, "v0.4.0", "")
	dir := t.TempDir()
	file, tag, err := fetchUpdate(ctx, http.DefaultClient, api, "0.3.0", "darwin", "arm64", dir)
	if err != nil || tag != "v0.4.0" {
		t.Fatalf("got %s %s, %v", file, tag, err)
	}
	if got, _ := os.ReadFile(file); string(got) != string(body) {
		t.Errorf("downloaded %q", got)
	}
	if fi, _ := os.Stat(file); filepath.Dir(file) != dir || fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("%s must be an executable in %s", file, dir)
	}

	if _, _, err := fetchUpdate(ctx, http.DefaultClient, api, "0.4.0+3", "darwin", "arm64", t.TempDir()); !errors.Is(err, errUpToDate) {
		t.Errorf("same version: %v", err)
	}
	if _, _, err := fetchUpdate(ctx, http.DefaultClient, api, "0.3.0", "linux", "arm64", t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "no build for this PC (linux/arm64)") {
		t.Errorf("missing asset: %v", err)
	}

	bad, _ := fakeGitHub(t, "v0.4.0", strings.Repeat("a", 64))
	empty := t.TempDir()
	if _, _, err := fetchUpdate(ctx, http.DefaultClient, bad, "0.3.0", "darwin", "arm64", empty); err == nil ||
		!strings.Contains(err.Error(), "checksum") {
		t.Errorf("checksum mismatch: %v", err)
	}
	if left, _ := os.ReadDir(empty); len(left) != 0 {
		t.Errorf("a failed download left %v", left)
	}
}

func TestCopyFileKeepsThePreviousExe(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "new"), filepath.Join(dir, "sevjala")
	os.WriteFile(src, []byte("v2"), 0o755)
	os.WriteFile(dst, []byte("v1"), 0o755)
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "v2" {
		t.Errorf("installed %q", got)
	}
	if got, _ := os.ReadFile(dst + ".old"); string(got) != "v1" {
		t.Errorf("kept %q", got)
	}
	if err := restoreExe(dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "v1" {
		t.Errorf("restored %q", got)
	}
}

func TestUpdateRollsBackWhenTheNewVersionDoesNotStart(t *testing.T) {
	var f fakeSetup
	f.failRuns = map[string]int{"service start": 1}
	if err := f.flow("1\n", "node", false).wizard(); err == nil {
		t.Error("a failed start must be reported")
	}
	want := []string{"run service status", "run service stop",
		`copy D:\Downloads\sevjala.exe → C:\Program Files\Sevjala\sevjala.exe`, "run service start",
		`restore C:\Program Files\Sevjala\sevjala.exe`, "run service start"}
	if !slices.Equal(f.calls, want) {
		t.Errorf("calls\n got %q\nwant %q", f.calls, want)
	}
}

// A tray still running from the previous .old keeps it locked on Windows; a
// non-empty folder stands in for that file here, since it cannot be removed.
func TestCopyFileMovesALockedOldAside(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "new"), filepath.Join(dir, "sevjala")
	os.WriteFile(src, []byte("v3"), 0o755)
	os.WriteFile(dst, []byte("v2"), 0o755)
	os.MkdirAll(filepath.Join(dst+".old", "busy"), 0o755)
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "v3" {
		t.Errorf("installed %q", got)
	}
	if got, _ := os.ReadFile(dst + ".old"); string(got) != "v2" {
		t.Errorf("kept %q", got)
	}
	if _, err := os.Stat(dst + ".old-1"); err != nil {
		t.Errorf("the locked .old was not moved aside: %v", err)
	}
}
