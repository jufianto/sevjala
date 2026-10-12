# P0 Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `sevjala` binary that installs itself as an OS service, runs an agent answering `GET /status` on the local pipe/socket, reports that via `service status` and `doctor`, and builds every asset of the matrix including the go-win7 ones.

**Architecture:** Cobra CLI in `cmd/` over four packages at the repo root: `config` (TOML + data dir), `logx` (slog JSON over a rotating file), `localapi` (HTTP over a named pipe / Unix socket, server + client), `svc` (kardianos/service wrapper, agent lifecycle, install helpers). GoReleaser builds stock assets with stock Go and Win7 assets with the go-win7 SDK via `tool:`.

**Tech Stack:** Go 1.27.2, cobra v1.10.2, kardianos/service v1.3.0, Microsoft/go-winio v0.6.3, BurntSushi/toml v1.6.0, golang.org/x/sys (windows token), go-winres v0.3.3 (tool), GoReleaser v2.18, golangci-lint v2.14, GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-10-12-p0-foundation-design.md` (read it with this plan). Blueprint: `docs/BLUEPRINT.md`.

## Global Constraints

- Module `github.com/jufianto/sevjala`; packages at the repo root, no `internal/` (D-001).
- `CGO_ENABLED=0` for every build; pure-Go dependencies only. Nothing may bind Windows APIs newer than Windows 7.
- Exit codes: `0` success, `1` operational error, `2` usage error.
- Service name `sevjala`, display name `Sevjala Agent`, description `Sevjala — Database Backup & Replication agent`, arguments `agent --data-dir <abs data dir>`.
- Data dir default: `%ProgramData%\Sevjala` / `/Library/Application Support/Sevjala` / `/var/lib/sevjala`; override `--data-dir`, then `SEVJALA_DATA_DIR`.
- Default endpoint `\\.\pipe\sevjala` (SDDL `D:P(A;;GA;;;BA)(A;;GA;;;SY)`) / `/var/run/sevjala.sock` (0660 root:sevjala). Non-default data dir → `<data-dir>/sevjala.sock` or `\\.\pipe\sevjala-<8 hex>` with the default DACL.
- Logs: `<data-dir>/logs/sevjala.log`, JSON lines, 10 MiB × 10 files, `event` attribute on events `agent_started`, `agent_stopped`, `service_installed`, `service_uninstalled`.
- Config defaults: `listen = "0.0.0.0:17891"`, `discovery_port = 17890`; unknown keys rejected.
- `install`/`uninstall`/`start`/`stop` need admin/root; `doctor` needs it too (it writes a probe file into the data dir).
- Comments: none unless the why is not visible from code; no ticket numbers.
- One atomic commit per task, conventional prefix (`feat`, `build`, `ci`, `docs`, `chore`), on branch `p0-foundation`.

## Review Focus

1. **Second `service install` while installed** → must refuse with "already installed; run 'sevjala service uninstall' first", not half-reinstall. Test in Task 6 (`TestInstallRefusesWhenInstalled`).
2. **Two agents on the same endpoint** (service running + a dev runs `sevjala agent`) → the second must fail fast naming the endpoint, never steal the socket. Test in Task 4 (`TestSecondListenerFails`).
3. **Stale socket after a crash** (`kill -9`) → next start must remove it and listen. Test in Task 4 (`TestStaleSocketRemoved`).
4. **Existing config with a different role** on install → refuse naming both roles; never rewrite. Test in Task 6 (`TestPrepareDataDir`).
5. **macOS Unix socket path length (104 bytes)** → tests must use a short `/tmp` dir; a too-long custom data dir must surface the listen error, not hang. Covered by `testDir` helper in Task 4 and `TestStartErrorsOnBadEndpoint`.

---

### Task 1: CLI skeleton, exit codes, tooling

**Files:**
- Create: `main.go`, `cmd/root.go`, `cmd/version.go`, `cmd/root_test.go`, `Makefile`, `.golangci.yml`

**Interfaces:**
- Produces: `cmd.Execute()`; `cmd.version` (string, set by ldflags); `run(args []string, out, errOut io.Writer) int`; `type exitError struct{code int; err error}`; `fail(err error) error`, `failf(format string, a ...any) error`, `usagef(format string, a ...any) error`, `errSilent1` (exit 1, prints nothing); `type globals struct{dataDirFlag string}` with `func (g *globals) dataDir() (string, error)` — added in Task 2, declared here as a field only.

- [ ] **Step 1: Add cobra**

Run: `go get github.com/spf13/cobra@v1.10.2`

- [ ] **Step 2: Write the failing test** — `cmd/root_test.go`

```go
package cmd

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		code    int
		stdout  string
		stderr  string
	}{
		{"version", []string{"version"}, 0, "sevjala dev (" + runtime.GOOS + "/" + runtime.GOARCH, ""},
		{"unknown command", []string{"nope"}, 2, "", "unknown command"},
		{"unknown flag", []string{"version", "--nope"}, 2, "", "unknown flag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(tt.args, &out, &errOut)
			if code != tt.code {
				t.Fatalf("exit = %d, want %d (stderr %q)", code, tt.code, errOut.String())
			}
			if !strings.Contains(out.String(), tt.stdout) {
				t.Errorf("stdout = %q, want it to contain %q", out.String(), tt.stdout)
			}
			if !strings.Contains(errOut.String(), tt.stderr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tt.stderr)
			}
		})
	}
}

func TestExitCodeFromErrors(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		code   int
		stderr string
	}{
		{"nil", nil, 0, ""},
		{"fail", failf("disk %s", "full"), 1, "sevjala: disk full\n"},
		{"usage", usagef("bad --role"), 2, "sevjala: bad --role\n"},
		{"silent", errSilent1, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var errOut bytes.Buffer
			if got := exitCode(tt.err, &errOut); got != tt.code {
				t.Errorf("code = %d, want %d", got, tt.code)
			}
			if errOut.String() != tt.stderr {
				t.Errorf("stderr = %q, want %q", errOut.String(), tt.stderr)
			}
		})
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./cmd/`
Expected: FAIL — `undefined: run`, `undefined: exitCode`, …

- [ ] **Step 4: Implement** — `main.go`

```go
package main

import "github.com/jufianto/sevjala/cmd"

func main() {
	cmd.Execute()
}
```

`cmd/root.go`

```go
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var version = "dev"

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// errSilent1 exits 1 without a message; the command already printed why.
var errSilent1 = &exitError{code: 1}

func fail(err error) error { return &exitError{code: 1, err: err} }

func failf(format string, a ...any) error { return fail(fmt.Errorf(format, a...)) }

func usagef(format string, a ...any) error {
	return &exitError{code: 2, err: fmt.Errorf(format, a...)}
}

type globals struct {
	dataDirFlag string
}

func newRootCmd() *cobra.Command {
	g := &globals{}
	root := &cobra.Command{
		Use:           "sevjala",
		Short:         "Sevjala — Database Backup & Replication",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&g.dataDirFlag, "data-dir", "", "data directory (default: $SEVJALA_DATA_DIR, else the OS default)")
	root.AddCommand(newVersionCmd())
	return root
}

func Execute() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, out, errOut io.Writer) int {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(out)
	root.SetErr(errOut)
	return exitCode(root.Execute(), errOut)
}

// exitCode maps an error to the process exit code. Errors our commands return
// are *exitError; anything else comes from cobra's parsing and is a usage error.
func exitCode(err error, errOut io.Writer) int {
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		if ee.err != nil {
			fmt.Fprintf(errOut, "sevjala: %v\n", ee.err)
		}
		return ee.code
	}
	fmt.Fprintf(errOut, "sevjala: %v\n", err)
	return 2
}
```

`cmd/version.go`

```go
package cmd

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			fmt.Fprintf(c.OutOrStdout(), "sevjala %s (%s/%s, %s)\n", version, runtime.GOOS, runtime.GOARCH, runtime.Version())
			return nil
		},
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/`
Expected: PASS

- [ ] **Step 6: Tooling** — `.golangci.yml`

```yaml
version: "2"
linters:
  default: standard
  enable:
    - errorlint
    - misspell
  exclusions:
    presets:
      - std-error-handling
    rules:
      - path: _test\.go
        linters: [errcheck]
formatters:
  enable:
    - gofmt
```

`Makefile` (Task 8 adds the release targets)

```make
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/jufianto/sevjala/cmd.version=$(VERSION)
RACE := $(if $(filter windows,$(shell go env GOOS)),,-race)

.PHONY: build test lint

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o sevjala .

test:
	go test $(RACE) ./...

lint:
	golangci-lint run
```

- [ ] **Step 7: Verify**

Run: `go mod tidy && make build && ./sevjala version && make test && make lint`
Expected: `sevjala <git describe> (darwin/arm64, go1.27.2)`, tests PASS, lint `0 issues.`

- [ ] **Step 8: Commit**

```bash
git add main.go cmd/ Makefile .golangci.yml go.mod go.sum
git commit -m "feat(cmd): sevjala CLI root with version and exit codes 0/1/2"
```

---

### Task 2: config — TOML config and data dir

**Files:**
- Create: `config/config.go`, `config/paths.go`, `config/config_test.go`, `config/paths_test.go`
- Modify: `cmd/root.go` (add `globals.dataDir`)

**Interfaces:**
- Produces:
  - `config.FileName = "config.toml"`, `config.RoleController`, `config.RoleNode`, `config.DefaultListen`, `config.DefaultDiscoveryPort`, `config.EnvDataDir = "SEVJALA_DATA_DIR"`, `config.SocketGroup = "sevjala"`
  - `type config.Config struct{Role, Listen string; DiscoveryPort int; Controller Controller; Node Node}`
  - `config.Default(role string) Config`, `config.ValidRole(role string) bool`, `(Config) Validate() error`
  - `config.Load(path string) (Config, error)` — missing file wraps `fs.ErrNotExist`
  - `config.Write(path string, c Config) error`
  - `config.DefaultDataDir() string`, `config.ResolveDataDir(flag string) (string, error)`, `config.IsDefaultDataDir(dir string) bool`, `config.Path(dataDir string) string`, `config.LogDir(dataDir string) string`
  - `(*cmd.globals).dataDir() (string, error)`

- [ ] **Step 1: Add toml**

Run: `go get github.com/BurntSushi/toml@v1.6.0`

- [ ] **Step 2: Write the failing tests** — `config/config_test.go`

```go
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    Config
		wantErr string
	}{
		{
			name:    "controller minimal gets defaults",
			content: `role = "controller"`,
			want:    Default(RoleController),
		},
		{
			name: "blueprint example",
			content: `role = "controller"
listen = "0.0.0.0:17891"
discovery_port = 17890

[controller.database]
host = "127.0.0.1"
port = 3306
user = "root"
password_ref = "keyring:db"
dumper = "C:\\xampp\\mysql\\bin\\mysqldump.exe"
databases = ["production_db"]

[controller.staging]
keep = 12

[node.store]
path = "D:\\SevjalaBackups"
min_free_bytes = 2147483648
`,
			want: Config{
				Role: RoleController, Listen: DefaultListen, DiscoveryPort: DefaultDiscoveryPort,
				Controller: Controller{
					Database: Database{Host: "127.0.0.1", Port: 3306, User: "root", PasswordRef: "keyring:db",
						Dumper: `C:\xampp\mysql\bin\mysqldump.exe`, Databases: []string{"production_db"}},
					Staging: Staging{Keep: 12},
				},
				Node: Node{Store: Store{Path: `D:\SevjalaBackups`, MinFreeBytes: 2147483648}},
			},
		},
		{name: "missing role", content: `listen = "0.0.0.0:1"`, wantErr: `role ""`},
		{name: "bad role", content: `role = "master"`, wantErr: `role "master"`},
		{name: "listen port out of range", content: "role = \"node\"\nlisten = \"0.0.0.0:99999\"", wantErr: "port must be 1-65535"},
		{name: "listen without port", content: "role = \"node\"\nlisten = \"0.0.0.0\"", wantErr: `listen "0.0.0.0"`},
		{name: "discovery port zero", content: "role = \"node\"\ndiscovery_port = 0", wantErr: "discovery_port 0"},
		{name: "unknown top key", content: "role = \"node\"\nrol = \"x\"", wantErr: `unknown key "rol"`},
		{name: "unknown nested key", content: "role = \"node\"\n[node.store]\npth = \"x\"", wantErr: `unknown key "node.store.pth"`},
		{name: "not toml", content: "role = ", wantErr: "read "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Load(writeFile(t, tt.content))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.Role != tt.want.Role || got.Listen != tt.want.Listen || got.DiscoveryPort != tt.want.DiscoveryPort ||
				got.Controller.Database.Dumper != tt.want.Controller.Database.Dumper ||
				got.Controller.Staging.Keep != tt.want.Controller.Staging.Keep ||
				got.Node.Store != tt.want.Node.Store {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), FileName))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestWriteRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := Write(path, Default(RoleNode)); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Role != RoleNode || got.Listen != DefaultListen || got.DiscoveryPort != DefaultDiscoveryPort {
		t.Errorf("round trip = %+v", got)
	}
}
```

`config/paths_test.go`

```go
package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	tests := []struct {
		goos, programData, want string
	}{
		{"windows", `D:\PD`, `D:\PD\Sevjala`},
		{"windows", "", `C:\ProgramData\Sevjala`},
		{"darwin", "", "/Library/Application Support/Sevjala"},
		{"linux", "", "/var/lib/sevjala"},
	}
	for _, tt := range tests {
		if got := defaultDataDir(tt.goos, tt.programData); got != tt.want {
			t.Errorf("defaultDataDir(%q, %q) = %q, want %q", tt.goos, tt.programData, got, tt.want)
		}
	}
}

func TestResolveDataDir(t *testing.T) {
	flagDir := t.TempDir()
	envDir := t.TempDir()

	t.Setenv(EnvDataDir, envDir)
	if got, _ := ResolveDataDir(flagDir); got != flagDir {
		t.Errorf("flag: got %q, want %q", got, flagDir)
	}
	if got, _ := ResolveDataDir(""); got != envDir {
		t.Errorf("env: got %q, want %q", got, envDir)
	}

	t.Setenv(EnvDataDir, "")
	if got, _ := ResolveDataDir(""); got != DefaultDataDir() {
		t.Errorf("default: got %q, want %q", got, DefaultDataDir())
	}

	got, err := ResolveDataDir("rel")
	if err != nil || !filepath.IsAbs(got) {
		t.Errorf("relative flag: got %q, %v; want an absolute path", got, err)
	}
	if !IsDefaultDataDir(DefaultDataDir()) || IsDefaultDataDir(flagDir) {
		t.Error("IsDefaultDataDir wrong")
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./config/`
Expected: FAIL — undefined: `Load`, `Default`, …

- [ ] **Step 4: Implement** — `config/config.go`

```go
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/BurntSushi/toml"
)

const (
	FileName             = "config.toml"
	RoleController       = "controller"
	RoleNode             = "node"
	DefaultListen        = "0.0.0.0:17891"
	DefaultDiscoveryPort = 17890
)

type Config struct {
	Role          string     `toml:"role"`
	Listen        string     `toml:"listen"`
	DiscoveryPort int        `toml:"discovery_port"`
	Controller    Controller `toml:"controller"`
	Node          Node       `toml:"node"`
}

type Controller struct {
	Database Database `toml:"database"`
	Staging  Staging  `toml:"staging"`
}

type Database struct {
	Host        string   `toml:"host"`
	Port        int      `toml:"port"`
	User        string   `toml:"user"`
	PasswordRef string   `toml:"password_ref"`
	Dumper      string   `toml:"dumper"`
	Databases   []string `toml:"databases"`
}

type Staging struct {
	Keep int `toml:"keep"`
}

type Node struct {
	Store Store `toml:"store"`
}

type Store struct {
	Path         string `toml:"path"`
	MinFreeBytes int64  `toml:"min_free_bytes"`
}

func Default(role string) Config {
	return Config{Role: role, Listen: DefaultListen, DiscoveryPort: DefaultDiscoveryPort}
}

func ValidRole(role string) bool {
	return role == RoleController || role == RoleNode
}

func (c Config) Validate() error {
	if !ValidRole(c.Role) {
		return fmt.Errorf("role %q: must be %q or %q", c.Role, RoleController, RoleNode)
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen %q: %w", c.Listen, err)
	}
	if p, err := strconv.Atoi(port); err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("listen %q: port must be 1-65535", c.Listen)
	}
	if c.DiscoveryPort < 1 || c.DiscoveryPort > 65535 {
		return fmt.Errorf("discovery_port %d: must be 1-65535", c.DiscoveryPort)
	}
	return nil
}

// Load reads and validates a config file; keys it omits keep their defaults.
func Load(path string) (Config, error) {
	c := Default("")
	md, err := toml.DecodeFile(path, &c)
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return Config{}, fmt.Errorf("%s: unknown key %q", path, keys[0].String())
	}
	if err := c.Validate(); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Write writes the top-level keys only; role sections are added by the
// milestones that use them.
func Write(path string, c Config) error {
	data := fmt.Sprintf("role = %q\nlisten = %q\ndiscovery_port = %d\n", c.Role, c.Listen, c.DiscoveryPort)
	return os.WriteFile(path, []byte(data), 0o644)
}
```

`config/paths.go`

```go
package config

import (
	"os"
	"path/filepath"
	"runtime"
)

const (
	EnvDataDir  = "SEVJALA_DATA_DIR"
	SocketGroup = "sevjala"
)

func DefaultDataDir() string {
	return defaultDataDir(runtime.GOOS, os.Getenv("ProgramData"))
}

func defaultDataDir(goos, programData string) string {
	switch goos {
	case "windows":
		if programData == "" {
			programData = `C:\ProgramData`
		}
		return programData + `\Sevjala`
	case "darwin":
		return "/Library/Application Support/Sevjala"
	default:
		return "/var/lib/sevjala"
	}
}

// ResolveDataDir picks the --data-dir flag, then $SEVJALA_DATA_DIR, then the
// OS default, and returns it as an absolute path.
func ResolveDataDir(flag string) (string, error) {
	dir := flag
	if dir == "" {
		dir = os.Getenv(EnvDataDir)
	}
	if dir == "" {
		return DefaultDataDir(), nil
	}
	return filepath.Abs(dir)
}

func IsDefaultDataDir(dir string) bool {
	return filepath.Clean(dir) == filepath.Clean(DefaultDataDir())
}

func Path(dataDir string) string { return filepath.Join(dataDir, FileName) }

func LogDir(dataDir string) string { return filepath.Join(dataDir, "logs") }
```

Add to `cmd/root.go` (import `github.com/jufianto/sevjala/config`):

```go
func (g *globals) dataDir() (string, error) {
	return config.ResolveDataDir(g.dataDirFlag)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./config/ ./cmd/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add config/ cmd/root.go go.mod go.sum
git commit -m "feat(config): config.toml load/validate/write and per-OS data dir"
```

---

### Task 3: logx — rotating JSON log

**Files:**
- Create: `logx/rotate.go`, `logx/logx.go`, `logx/rotate_test.go`, `logx/logx_test.go`

**Interfaces:**
- Produces:
  - `logx.NewRotatingWriter(path string, maxBytes int64, keep int) (*RotatingWriter, error)`; `(*RotatingWriter).Write([]byte) (int, error)`; `(*RotatingWriter).Close() error`
  - `logx.MaxBytes = 10 << 20`, `logx.Keep = 10`, `logx.FileName = "sevjala.log"`
  - `logx.EventAgentStarted`, `logx.EventAgentStopped`, `logx.EventServiceInstalled`, `logx.EventServiceUninstalled`
  - `logx.Event(name string) slog.Attr`
  - `logx.Open(dir string, foreground bool) (*slog.Logger, io.Closer, error)`

- [ ] **Step 1: Write the failing tests** — `logx/rotate_test.go`

```go
package logx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	w, err := NewRotatingWriter(path, 100, 3)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{"a", "b", "c", "d"} {
		if _, err := w.Write([]byte(strings.Repeat(c, 60))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	want := map[string]string{path: "d", path + ".1": "c", path + ".2": "b"}
	for p, c := range want {
		if got := read(t, p); got != strings.Repeat(c, 60) {
			t.Errorf("%s = %.5q…, want %q×60", filepath.Base(p), got, c)
		}
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Errorf("%s.3 exists; keep=3 means current + .1 + .2", FileName)
	}
}

func TestOversizedWriteOnEmptyFileDoesNotRotate(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	w, err := NewRotatingWriter(path, 10, 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(strings.Repeat("x", 50))); err != nil {
		t.Fatal(err)
	}
	w.Close()
	if _, err := os.Stat(path + ".1"); !os.IsNotExist(err) {
		t.Error("rotated an empty file")
	}
}

func TestReopenAppends(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	for _, s := range []string{"one\n", "two\n"} {
		w, err := NewRotatingWriter(path, 100, 3)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(s))
		w.Close()
	}
	if got := read(t, path); got != "one\ntwo\n" {
		t.Errorf("got %q", got)
	}
}
```

`logx/logx_test.go`

```go
package logx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenWritesJSONEvents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	log, closer, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("agent started", Event(EventAgentStarted), "role", "node")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &rec); err != nil {
		t.Fatalf("not one JSON line: %q", b)
	}
	if rec["event"] != EventAgentStarted || rec["role"] != "node" || rec["msg"] != "agent started" {
		t.Errorf("record = %v", rec)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./logx/`
Expected: FAIL — undefined: `NewRotatingWriter`, `Open`, …

- [ ] **Step 3: Implement** — `logx/rotate.go`

```go
package logx

import (
	"fmt"
	"os"
	"sync"
)

// RotatingWriter appends to path and, when a write would pass maxBytes,
// shifts path → path.1 → … → path.<keep-1>, dropping the oldest.
type RotatingWriter struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

func NewRotatingWriter(path string, maxBytes int64, keep int) (*RotatingWriter, error) {
	w := &RotatingWriter{path: path, max: maxBytes, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, info.Size()
	return nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(p)) > w.max {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes before renaming because Windows cannot rename an open file.
// Rename failures are skipped so logging continues in the current file.
func (w *RotatingWriter) rotate() error {
	w.f.Close()
	w.f = nil
	_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.keep-1))
	for i := w.keep - 2; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	_ = os.Rename(w.path, w.path+".1")
	return w.open()
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
```

`logx/logx.go`

```go
package logx

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	MaxBytes = 10 << 20
	Keep     = 10
	FileName = "sevjala.log"
)

// Event names are provisional until the PRD's event list (section 25) is in the repo.
const (
	EventAgentStarted       = "agent_started"
	EventAgentStopped       = "agent_stopped"
	EventServiceInstalled   = "service_installed"
	EventServiceUninstalled = "service_uninstalled"
)

func Event(name string) slog.Attr { return slog.String("event", name) }

// Open logs JSON lines to dir/sevjala.log; foreground also prints text to stderr.
func Open(dir string, foreground bool) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	w, err := NewRotatingWriter(filepath.Join(dir, FileName), MaxBytes, Keep)
	if err != nil {
		return nil, nil, err
	}
	var h slog.Handler = slog.NewJSONHandler(w, nil)
	if foreground {
		h = slog.NewMultiHandler(h, slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(h), w, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./logx/`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add logx/
git commit -m "feat(logx): JSON log with size rotation, 10 MiB × 10 files, event attribute"
```

---

### Task 4: localapi — pipe/socket server and client

**Files:**
- Create: `localapi/endpoint.go`, `localapi/endpoint_unix.go`, `localapi/endpoint_windows.go`, `localapi/server.go`, `localapi/client.go`, `localapi/localapi_test.go`, `localapi/endpoint_unix_test.go`

**Interfaces:**
- Consumes: `config.IsDefaultDataDir`, `config.SocketGroup`
- Produces:
  - `type localapi.Status struct{Version string "version"; Role string "role"; PID int "pid"; UptimeS int64 "uptime_s"; DataDir string "data_dir"}`
  - `localapi.Endpoint(dataDir string) string`
  - `localapi.NewHandler(status func() Status) http.Handler`
  - `localapi.Start(endpoint string, h http.Handler, log *slog.Logger) (*Server, error)`; `(*Server).Shutdown(ctx context.Context) error`
  - `localapi.NewClient(endpoint string) *Client`; `(*Client).Status(ctx context.Context) (Status, error)`

- [ ] **Step 1: Add go-winio**

Run: `go get github.com/Microsoft/go-winio@v0.6.3`

- [ ] **Step 2: Write the failing tests** — `localapi/localapi_test.go`

```go
package localapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
)

// testDir is short on purpose: macOS caps Unix socket paths at 104 bytes and
// t.TempDir() under /var/folders often exceeds it.
func testDir(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if runtime.GOOS != "windows" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "sj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

var discard = slog.New(slog.DiscardHandler)

func startTest(t *testing.T, st Status) string {
	t.Helper()
	ep := Endpoint(testDir(t))
	srv, err := Start(ep, NewHandler(func() Status { return st }), discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	return ep
}

func TestStatusRoundTrip(t *testing.T) {
	want := Status{Version: "0.1.0", Role: "node", PID: 42, UptimeS: 7, DataDir: "/x"}
	ep := startTest(t, want)
	got, err := NewClient(ep).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestUnknownPathIs404JSON(t *testing.T) {
	ep := startTest(t, Status{})
	resp, err := NewClient(ep).hc.Get("http://sevjala/nope")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var m map[string]string
	if resp.StatusCode != http.StatusNotFound || json.Unmarshal(body, &m) != nil || m["error"] != "not found" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
}

func TestSecondListenerFails(t *testing.T) {
	ep := startTest(t, Status{})
	if srv, err := Start(ep, NewHandler(func() Status { return Status{} }), discard); err == nil {
		srv.Shutdown(context.Background())
		t.Fatal("second Start on the same endpoint succeeded")
	} else if !strings.Contains(err.Error(), ep) {
		t.Errorf("error %q does not name the endpoint", err)
	}
}

func TestClientWithoutServerFails(t *testing.T) {
	if _, err := NewClient(Endpoint(testDir(t))).Status(context.Background()); err == nil {
		t.Fatal("Status succeeded with no server")
	}
}

func TestEndpointMovesWithCustomDataDir(t *testing.T) {
	a, b := Endpoint(testDir(t)), Endpoint(testDir(t))
	if a == defaultEndpoint || a == b {
		t.Errorf("custom endpoints %q, %q must differ from each other and from %q", a, b, defaultEndpoint)
	}
}
```

`localapi/endpoint_unix_test.go`

```go
//go:build !windows

package localapi

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestStaleSocketRemoved(t *testing.T) {
	ep := Endpoint(testDir(t))
	ln, err := net.Listen("unix", ep)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()

	srv, err := Start(ep, NewHandler(func() Status { return Status{} }), discard)
	if err != nil {
		t.Fatalf("stale socket blocked Start: %v", err)
	}
	srv.Shutdown(context.Background())
	if _, err := os.Stat(ep); !os.IsNotExist(err) {
		t.Error("socket left behind after Shutdown")
	}
}

func TestSocketPermissions(t *testing.T) {
	ep := startTest(t, Status{})
	info, err := os.Stat(ep)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o600)
	if os.Geteuid() == 0 {
		want = 0o660
	}
	if info.Mode().Perm() != want {
		t.Errorf("mode = %v, want %v", info.Mode().Perm(), want)
	}
}

func TestStartErrorsOnBadEndpoint(t *testing.T) {
	ep := filepath.Join(testDir(t), "missing-dir", "sevjala.sock")
	if _, err := Start(ep, NewHandler(func() Status { return Status{} }), discard); err == nil {
		t.Fatal("Start succeeded in a missing directory")
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./localapi/`
Expected: FAIL — undefined: `Endpoint`, `Start`, …

- [ ] **Step 4: Implement** — `localapi/endpoint.go`

```go
package localapi

import "github.com/jufianto/sevjala/config"

// Endpoint is the pipe or socket for a data dir. A non-default data dir gets
// its own endpoint so dev and test agents never collide with the service.
func Endpoint(dataDir string) string {
	if config.IsDefaultDataDir(dataDir) {
		return defaultEndpoint
	}
	return customEndpoint(dataDir)
}
```

`localapi/endpoint_unix.go`

```go
//go:build !windows

package localapi

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jufianto/sevjala/config"
)

const defaultEndpoint = "/var/run/sevjala.sock"

func customEndpoint(dataDir string) string {
	return filepath.Join(dataDir, "sevjala.sock")
}

// listen returns a warning when running as root without the sevjala group,
// in which case the socket stays root-only.
func listen(endpoint string) (net.Listener, string, error) {
	if c, err := net.DialTimeout("unix", endpoint, time.Second); err == nil {
		c.Close()
		return nil, "", errors.New("another agent is already listening")
	}
	if err := os.Remove(endpoint); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, "", err
	}
	ln, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, "", err
	}
	mode, warn := os.FileMode(0o600), ""
	if os.Geteuid() == 0 {
		if g, err := user.LookupGroup(config.SocketGroup); err == nil {
			gid, _ := strconv.Atoi(g.Gid)
			if err := os.Chown(endpoint, 0, gid); err != nil {
				ln.Close()
				return nil, "", err
			}
			mode = 0o660
		} else {
			warn = fmt.Sprintf("group %q not found; local API is root-only", config.SocketGroup)
		}
	}
	if err := os.Chmod(endpoint, mode); err != nil {
		ln.Close()
		return nil, "", err
	}
	return ln, warn, nil
}

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", endpoint)
}
```

`localapi/endpoint_windows.go`

```go
//go:build windows

package localapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"path/filepath"

	"github.com/Microsoft/go-winio"
)

const defaultEndpoint = `\\.\pipe\sevjala`

// Administrators and SYSTEM only (blueprint security model, Local API row).
const defaultSDDL = "D:P(A;;GA;;;BA)(A;;GA;;;SY)"

func customEndpoint(dataDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(dataDir)))
	return defaultEndpoint + "-" + hex.EncodeToString(sum[:4])
}

// Custom endpoints keep the default DACL so an un-elevated developer can
// reach the agent they started.
func listen(endpoint string) (net.Listener, string, error) {
	cfg := &winio.PipeConfig{}
	if endpoint == defaultEndpoint {
		cfg.SecurityDescriptor = defaultSDDL
	}
	ln, err := winio.ListenPipe(endpoint, cfg)
	return ln, "", err
}

func dial(ctx context.Context, endpoint string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, endpoint)
}
```

`localapi/server.go`

```go
package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type Status struct {
	Version string `json:"version"`
	Role    string `json:"role"`
	PID     int    `json:"pid"`
	UptimeS int64  `json:"uptime_s"`
	DataDir string `json:"data_dir"`
}

func NewHandler(status func() Status) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, status())
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	return mux
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type Server struct {
	srv *http.Server
}

func Start(endpoint string, h http.Handler, log *slog.Logger) (*Server, error) {
	ln, warn, err := listen(endpoint)
	if err != nil {
		return nil, fmt.Errorf("local api %s: %w", endpoint, err)
	}
	if warn != "" {
		log.Warn(warn)
	}
	s := &Server{srv: &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("local api stopped", "err", err)
		}
	}()
	return s, nil
}

// Shutdown closes the listener, which also removes a Unix socket file.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}
```

`localapi/client.go`

```go
package localapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

type Client struct {
	hc *http.Client
}

func NewClient(endpoint string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, endpoint)
		},
	}
	return &Client{hc: &http.Client{Transport: tr, Timeout: 3 * time.Second}}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://sevjala/status", nil)
	if err != nil {
		return Status{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return Status{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Status{}, fmt.Errorf("GET /status: %s", resp.Status)
	}
	var st Status
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return Status{}, fmt.Errorf("GET /status: %w", err)
	}
	return st, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass, on this host and cross-compiled for Windows**

Run: `go test -race ./localapi/ && GOOS=windows go vet ./localapi/`
Expected: PASS; vet clean (Windows tests run in CI).

- [ ] **Step 6: Commit**

```bash
git add localapi/ go.mod go.sum
git commit -m "feat(localapi): HTTP over named pipe / Unix socket with GET /status and client"
```

---

### Task 5: svc agent lifecycle and `sevjala agent`

**Files:**
- Create: `svc/svc.go`, `svc/agent.go`, `svc/agent_test.go`, `cmd/agent.go`
- Modify: `cmd/root.go` (register `newAgentCmd(g)`)

**Interfaces:**
- Consumes: `config.*`, `logx.Open`, `logx.Event*`, `localapi.Start/NewHandler/Endpoint/Status`
- Produces:
  - `svc.Name = "sevjala"`
  - `svc.New(dataDir string, i service.Interface) (service.Service, error)`
  - `type svc.Agent struct{DataDir string; Config config.Config; Version string; Foreground bool}` implementing `Start(service.Service) error`, `Stop(service.Service) error`

- [ ] **Step 1: Add kardianos/service**

Run: `go get github.com/kardianos/service@v1.3.0`

- [ ] **Step 2: Write the failing test** — `svc/agent_test.go`

```go
package svc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

func shortDir(t *testing.T) string {
	t.Helper()
	base := os.TempDir()
	if runtime.GOOS != "windows" {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "sj")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func TestAgentStartServesStatusAndLogs(t *testing.T) {
	dir := shortDir(t)
	a := &Agent{DataDir: dir, Config: config.Default(config.RoleNode), Version: "test"}
	if err := a.Start(nil); err != nil {
		t.Fatal(err)
	}

	st, err := localapi.NewClient(localapi.Endpoint(dir)).Status(context.Background())
	if err != nil {
		a.Stop(nil)
		t.Fatal(err)
	}
	if st.Role != config.RoleNode || st.Version != "test" || st.PID != os.Getpid() || st.DataDir != dir {
		t.Errorf("status = %+v", st)
	}

	if err := a.Stop(nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(config.LogDir(dir), logx.FileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{logx.EventAgentStarted, logx.EventAgentStopped} {
		if !strings.Contains(string(b), `"event":"`+ev+`"`) {
			t.Errorf("log lacks %s: %s", ev, b)
		}
	}
}

func TestAgentStartFailsWhenEndpointTaken(t *testing.T) {
	dir := shortDir(t)
	first := &Agent{DataDir: dir, Config: config.Default(config.RoleNode)}
	if err := first.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer first.Stop(nil)
	second := &Agent{DataDir: dir, Config: config.Default(config.RoleNode)}
	if err := second.Start(nil); err == nil {
		second.Stop(nil)
		t.Fatal("second agent started on a taken endpoint")
	}
}

func TestNewServiceConfig(t *testing.T) {
	s, err := New("/data", &Agent{})
	if err != nil {
		t.Fatal(err)
	}
	if s.String() != "Sevjala Agent" {
		t.Errorf("display name = %q", s.String())
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./svc/`
Expected: FAIL — undefined: `Agent`, `New`

- [ ] **Step 4: Implement** — `svc/svc.go`

```go
package svc

import (
	"runtime"

	"github.com/kardianos/service"
)

const Name = "sevjala"

func New(dataDir string, i service.Interface) (service.Service, error) {
	cfg := &service.Config{
		Name:        Name,
		DisplayName: "Sevjala Agent",
		Description: "Sevjala — Database Backup & Replication agent",
		Arguments:   []string{"agent", "--data-dir", dataDir},
		Option: service.KeyValue{
			"RunAtLoad":              true,
			"KeepAlive":              true,
			"Restart":                "on-failure",
			"OnFailure":              "restart",
			"OnFailureDelayDuration": "5s",
		},
	}
	// On Windows, Dependencies are service names, not systemd lines.
	if runtime.GOOS == "linux" {
		cfg.Dependencies = []string{"After=network-online.target", "Wants=network-online.target"}
	}
	return service.New(i, cfg)
}
```

`svc/agent.go`

```go
package svc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

type Agent struct {
	DataDir    string
	Config     config.Config
	Version    string
	Foreground bool

	log     *slog.Logger
	logFile io.Closer
	api     *localapi.Server
	started time.Time
}

// Start must not block: kardianos/service calls it on the service thread.
func (a *Agent) Start(service.Service) error {
	log, closer, err := logx.Open(config.LogDir(a.DataDir), a.Foreground)
	if err != nil {
		return err
	}
	a.log, a.logFile, a.started = log, closer, time.Now()

	endpoint := localapi.Endpoint(a.DataDir)
	api, err := localapi.Start(endpoint, localapi.NewHandler(a.status), log)
	if err != nil {
		log.Error("agent failed to start", "err", err)
		closer.Close()
		return err
	}
	a.api = api
	log.Info("agent started", logx.Event(logx.EventAgentStarted),
		"role", a.Config.Role, "version", a.Version, "endpoint", endpoint, "data_dir", a.DataDir)
	return nil
}

func (a *Agent) Stop(service.Service) error {
	if a.api == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := a.api.Shutdown(ctx)
	a.api = nil
	a.log.Info("agent stopped", logx.Event(logx.EventAgentStopped))
	return errors.Join(err, a.logFile.Close())
}

func (a *Agent) status() localapi.Status {
	return localapi.Status{
		Version: a.Version,
		Role:    a.Config.Role,
		PID:     os.Getpid(),
		UptimeS: int64(time.Since(a.started).Seconds()),
		DataDir: a.DataDir,
	}
}
```

`cmd/agent.go`

```go
package cmd

import (
	"errors"
	"io/fs"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/svc"
)

func newAgentCmd(g *globals) *cobra.Command {
	var role string
	c := &cobra.Command{
		Use:   "agent",
		Short: "Run the agent in the foreground (the service runs this)",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if role != "" && !config.ValidRole(role) {
				return usagef("--role must be %q or %q", config.RoleController, config.RoleNode)
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			cfg, err := config.Load(config.Path(dataDir))
			switch {
			case err == nil:
			case errors.Is(err, fs.ErrNotExist) && role != "":
				cfg = config.Default(role)
			case errors.Is(err, fs.ErrNotExist):
				return failf("not installed: run 'sevjala service install --role %s|%s'", config.RoleController, config.RoleNode)
			default:
				return fail(err)
			}
			if role != "" {
				cfg.Role = role
			}
			a := &svc.Agent{DataDir: dataDir, Config: cfg, Version: version, Foreground: service.Interactive()}
			s, err := svc.New(dataDir, a)
			if err != nil {
				return fail(err)
			}
			if err := s.Run(); err != nil {
				return fail(err)
			}
			return nil
		},
	}
	c.Flags().StringVar(&role, "role", "", "override the configured role: controller or node")
	return c
}
```

Register in `newRootCmd`: `root.AddCommand(newVersionCmd(), newAgentCmd(g))`.

- [ ] **Step 5: Run the tests and a manual foreground run**

Run: `go test -race ./svc/ ./cmd/`
Expected: PASS

Run: `make build && ./sevjala --data-dir /tmp/sjdev agent --role node & sleep 1; curl -s --unix-socket /tmp/sjdev/sevjala.sock http://x/status; kill $!`
Expected: JSON `{"version":"…","role":"node","pid":…,"uptime_s":…,"data_dir":"/tmp/sjdev"}`, then an `agent stopped` text line on stderr.

Run: `./sevjala --data-dir /tmp/sjnone agent; echo "exit $?"`
Expected: `sevjala: not installed: run 'sevjala service install --role controller|node'`, `exit 1`

- [ ] **Step 6: Commit**

```bash
git add svc/ cmd/agent.go cmd/root.go go.mod go.sum
git commit -m "feat(svc): agent lifecycle under kardianos/service; sevjala agent"
```

---

### Task 6: `sevjala service install|uninstall|start|stop|status`

**Files:**
- Create: `svc/install.go`, `svc/install_test.go`, `svc/group.go`, `svc/group_test.go`, `svc/admin_unix.go`, `svc/admin_windows.go`, `cmd/service.go`, `cmd/status.go`, `cmd/status_test.go`, `cmd/service_test.go`
- Modify: `cmd/root.go` (register `newServiceCmd(g)`)

**Interfaces:**
- Consumes: `svc.New`, `svc.Agent`, `config.*`, `localapi.NewClient/Endpoint/Status`, `logx.Open/Event*`
- Produces:
  - `svc.PrepareDataDir(dataDir, role string) (created bool, err error)`
  - `svc.EnsureGroup(user string) error`; `groupCommands(goos string, exists bool, user string) [][]string`
  - `svc.IsAdmin() bool`; `svc.AdminHint string`
  - `cmd`: `type serviceStatuser interface{ Status() (service.Status, error); Platform() string }`; `type statusView struct{Service string; ServiceUp bool; Platform string; Agent *localapi.Status; AgentErr error; DataDir string}`; `buildStatusView(s serviceStatuser, st localapi.Status, apiErr error, dataDir string, privileged bool) statusView`; `(statusView) OK() bool`; `renderStatus(w io.Writer, v statusView)`; `waitForAgent(c *localapi.Client, timeout time.Duration) (localapi.Status, error)`; `checkNotInstalled(s serviceStatuser) error`

- [ ] **Step 1: Add x/sys**

Run: `go get golang.org/x/sys@v0.49.0`

- [ ] **Step 2: Write the failing tests** — `svc/install_test.go`

```go
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
```

`svc/group_test.go`

```go
package svc

import (
	"reflect"
	"testing"
)

func TestGroupCommands(t *testing.T) {
	tests := []struct {
		goos   string
		exists bool
		user   string
		want   [][]string
	}{
		{"linux", false, "jufi", [][]string{{"groupadd", "--system", "sevjala"}, {"usermod", "-aG", "sevjala", "jufi"}}},
		{"linux", true, "", nil},
		{"darwin", false, "", [][]string{{"dseditgroup", "-o", "create", "-r", "Sevjala", "sevjala"}}},
		{"darwin", true, "jufi", [][]string{{"dseditgroup", "-o", "edit", "-a", "jufi", "-t", "user", "sevjala"}}},
		{"windows", false, "jufi", nil},
	}
	for _, tt := range tests {
		if got := groupCommands(tt.goos, tt.exists, tt.user); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("groupCommands(%s, %v, %q) = %v, want %v", tt.goos, tt.exists, tt.user, got, tt.want)
		}
	}
}
```

`cmd/status_test.go`

```go
package cmd

import (
	"bytes"
	"errors"
	"testing"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/localapi"
)

type fakeService struct {
	st  service.Status
	err error
}

func (f fakeService) Status() (service.Status, error) { return f.st, f.err }
func (f fakeService) Platform() string                { return "test-platform" }

var answering = localapi.Status{Version: "0.1.0", Role: "node", PID: 4120, UptimeS: 133, DataDir: "/d"}

func TestBuildStatusView(t *testing.T) {
	down := errors.New("dial: no such file")
	tests := []struct {
		name       string
		svc        fakeService
		apiErr     error
		privileged bool
		service    string
		ok         bool
	}{
		{"running and answering", fakeService{st: service.StatusRunning}, nil, true, "running", true},
		{"running, agent down", fakeService{st: service.StatusRunning}, down, true, "running", false},
		{"stopped", fakeService{st: service.StatusStopped}, down, true, "stopped", false},
		{"not installed", fakeService{err: service.ErrNotInstalled}, down, false, "not installed", false},
		{"not installed but a dev agent answers", fakeService{err: service.ErrNotInstalled}, nil, false, "not installed", false},
		{"unprivileged sees stopped but agent answers", fakeService{st: service.StatusStopped}, nil, false, "unknown (run with sudo for the service state)", true},
		{"query error", fakeService{err: errors.New("boom")}, down, true, "unknown (boom)", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := buildStatusView(tt.svc, answering, tt.apiErr, "/d", tt.privileged)
			if v.Service != tt.service || v.OK() != tt.ok {
				t.Errorf("service=%q ok=%v, want %q %v", v.Service, v.OK(), tt.service, tt.ok)
			}
		})
	}
}

func TestRenderStatus(t *testing.T) {
	var b bytes.Buffer
	renderStatus(&b, buildStatusView(fakeService{st: service.StatusRunning}, answering, nil, `C:\ProgramData\Sevjala`, true))
	want := "service   running      (test-platform)\n" +
		"agent     answering    0.1.0  role=node  up 2m13s  pid 4120\n" +
		"data dir  C:\\ProgramData\\Sevjala\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}

	b.Reset()
	renderStatus(&b, buildStatusView(fakeService{st: service.StatusStopped}, localapi.Status{}, errors.New("refused"), "/d", true))
	want = "service   stopped      (test-platform)\n" +
		"agent     not answering (refused)\n" +
		"data dir  /d\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
}
```

`cmd/service_test.go`

```go
package cmd

import (
	"strings"
	"testing"

	"github.com/kardianos/service"
)

func TestInstallRefusesWhenInstalled(t *testing.T) {
	err := checkNotInstalled(fakeService{st: service.StatusRunning})
	if err == nil || !strings.Contains(err.Error(), "already installed; run 'sevjala service uninstall' first") {
		t.Errorf("err = %v", err)
	}
	if err := checkNotInstalled(fakeService{err: service.ErrNotInstalled}); err != nil {
		t.Errorf("not installed: err = %v", err)
	}
}

func TestInstallRoleIsUsageError(t *testing.T) {
	for _, args := range [][]string{
		{"service", "install"},
		{"service", "install", "--role", "master"},
	} {
		var out, errOut strings.Builder
		if code := run(args, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2 (%s)", args, code, errOut.String())
		}
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `go test ./svc/ ./cmd/`
Expected: FAIL — undefined: `PrepareDataDir`, `groupCommands`, `buildStatusView`, `checkNotInstalled`, …

- [ ] **Step 4: Implement** — `svc/install.go`

```go
package svc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/jufianto/sevjala/config"
)

// PrepareDataDir creates the data and log dirs and writes a default config
// for role when none exists. It never rewrites an existing config.
func PrepareDataDir(dataDir, role string) (created bool, err error) {
	if err := os.MkdirAll(config.LogDir(dataDir), 0o755); err != nil {
		return false, err
	}
	path := config.Path(dataDir)
	cur, err := config.Load(path)
	switch {
	case err == nil:
		if cur.Role != role {
			return false, fmt.Errorf("%s already has role %q; refusing to install as %q", path, cur.Role, role)
		}
		return false, nil
	case errors.Is(err, fs.ErrNotExist):
		return true, config.Write(path, config.Default(role))
	default:
		return false, err
	}
}
```

`svc/group.go`

```go
package svc

import (
	"bytes"
	"fmt"
	"os/exec"
	"os/user"
	"runtime"
	"strings"

	"github.com/jufianto/sevjala/config"
)

// EnsureGroup creates the local API socket group and adds user to it.
// It does nothing on Windows, where the pipe ACL replaces the group.
func EnsureGroup(username string) error {
	_, err := user.LookupGroup(config.SocketGroup)
	for _, c := range groupCommands(runtime.GOOS, err == nil, username) {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(c, " "), err, bytes.TrimSpace(out))
		}
	}
	return nil
}

func groupCommands(goos string, exists bool, username string) [][]string {
	g := config.SocketGroup
	var cmds [][]string
	switch goos {
	case "linux":
		if !exists {
			cmds = append(cmds, []string{"groupadd", "--system", g})
		}
		if username != "" {
			cmds = append(cmds, []string{"usermod", "-aG", g, username})
		}
	case "darwin":
		if !exists {
			cmds = append(cmds, []string{"dseditgroup", "-o", "create", "-r", "Sevjala", g})
		}
		if username != "" {
			cmds = append(cmds, []string{"dseditgroup", "-o", "edit", "-a", username, "-t", "user", g})
		}
	}
	return cmds
}
```

`svc/admin_unix.go`

```go
//go:build !windows

package svc

import "os"

const AdminHint = "run with sudo"

func IsAdmin() bool { return os.Geteuid() == 0 }
```

`svc/admin_windows.go`

```go
//go:build windows

package svc

import "golang.org/x/sys/windows"

const AdminHint = "run this from an Administrator prompt"

func IsAdmin() bool { return windows.GetCurrentProcessToken().IsElevated() }
```

`cmd/status.go`

```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/localapi"
)

type serviceStatuser interface {
	Status() (service.Status, error)
	Platform() string
}

type statusView struct {
	Service   string
	ServiceUp bool
	Platform  string
	Agent     *localapi.Status
	AgentErr  error
	DataDir   string
}

func (v statusView) OK() bool { return v.ServiceUp && v.Agent != nil }

// buildStatusView treats an answering agent as proof the service runs when an
// unprivileged caller cannot see the service manager's state (macOS launchctl).
func buildStatusView(s serviceStatuser, st localapi.Status, apiErr error, dataDir string, privileged bool) statusView {
	v := statusView{Platform: s.Platform(), DataDir: dataDir}
	if apiErr == nil {
		v.Agent = &st
	} else {
		v.AgentErr = apiErr
	}
	state, err := s.Status()
	switch {
	case errors.Is(err, service.ErrNotInstalled):
		v.Service = "not installed"
	case err != nil:
		v.Service = "unknown (" + err.Error() + ")"
	case state == service.StatusRunning:
		v.Service, v.ServiceUp = "running", true
	case !privileged && v.Agent != nil:
		v.Service, v.ServiceUp = "unknown (run with sudo for the service state)", true
	default:
		v.Service = "stopped"
	}
	return v
}

func renderStatus(w io.Writer, v statusView) {
	fmt.Fprintf(w, "%-10s%-13s(%s)\n", "service", v.Service, v.Platform)
	if v.Agent != nil {
		a := v.Agent
		fmt.Fprintf(w, "%-10s%-13s%s  role=%s  up %s  pid %d\n", "agent", "answering",
			a.Version, a.Role, time.Duration(a.UptimeS)*time.Second, a.PID)
	} else {
		fmt.Fprintf(w, "%-10snot answering (%v)\n", "agent", v.AgentErr)
	}
	fmt.Fprintf(w, "%-10s%s\n", "data dir", v.DataDir)
}

func queryAgent(c *localapi.Client) (localapi.Status, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return c.Status(ctx)
}

func waitForAgent(c *localapi.Client, timeout time.Duration) (localapi.Status, error) {
	deadline := time.Now().Add(timeout)
	for {
		st, err := queryAgent(c)
		if err == nil || time.Now().After(deadline) {
			return st, err
		}
		time.Sleep(250 * time.Millisecond)
	}
}
```

`cmd/service.go`

```go
package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kardianos/service"
	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/svc"
)

const agentWait = 10 * time.Second

func newServiceCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "service", Short: "Install and control the Sevjala Agent service"}
	c.AddCommand(newServiceInstallCmd(g), newServiceUninstallCmd(g),
		newServiceStartCmd(g), newServiceStopCmd(g), newServiceStatusCmd(g))
	return c
}

func checkNotInstalled(s serviceStatuser) error {
	if _, err := s.Status(); errors.Is(err, service.ErrNotInstalled) {
		return nil
	}
	return errors.New("already installed; run 'sevjala service uninstall' first")
}

func requireAdmin() error {
	if !svc.IsAdmin() {
		return failf("%s", svc.AdminHint)
	}
	return nil
}

// logEvent writes one event to the agent log; called only while the agent is
// not running so two processes never rotate the same file.
func logEvent(dataDir, msg, event string, attrs ...any) {
	log, closer, err := logx.Open(config.LogDir(dataDir), false)
	if err != nil {
		return
	}
	log.Info(msg, append([]any{logx.Event(event)}, attrs...)...)
	closer.Close()
}

func printStatus(w io.Writer, s service.Service, dataDir string, st localapi.Status, apiErr error) statusView {
	v := buildStatusView(s, st, apiErr, dataDir, svc.IsAdmin())
	renderStatus(w, v)
	return v
}

func newServiceInstallCmd(g *globals) *cobra.Command {
	var role string
	c := &cobra.Command{
		Use:   "install --role controller|node",
		Short: "Create the data dir and config, register and start the service",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if !config.ValidRole(role) {
				return usagef("--role must be %q or %q", config.RoleController, config.RoleNode)
			}
			if err := requireAdmin(); err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			out := c.OutOrStdout()
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			if err := checkNotInstalled(s); err != nil {
				return fail(err)
			}
			created, err := svc.PrepareDataDir(dataDir, role)
			if err != nil {
				return fail(err)
			}
			if created {
				fmt.Fprintf(out, "wrote %s\n", config.Path(dataDir))
			}
			if runtime.GOOS != "windows" {
				sudoUser := os.Getenv("SUDO_USER")
				if err := svc.EnsureGroup(sudoUser); err != nil {
					return fail(err)
				}
				if sudoUser != "" {
					fmt.Fprintf(out, "added %s to group %s; log out and back in to use the CLI without sudo\n", sudoUser, config.SocketGroup)
				} else {
					fmt.Fprintf(out, "add operators to group %s to use the CLI without sudo\n", config.SocketGroup)
				}
			}
			exe, _ := os.Executable()
			if err := s.Install(); err != nil {
				return failf("install service: %w", err)
			}
			logEvent(dataDir, "service installed", logx.EventServiceInstalled, "role", role, "executable", exe)
			fmt.Fprintf(out, "installed service %s → %s\n", svc.Name, exe)
			if err := s.Start(); err != nil {
				return failf("start service: %w", err)
			}
			st, apiErr := waitForAgent(localapi.NewClient(localapi.Endpoint(dataDir)), agentWait)
			if v := printStatus(out, s, dataDir, st, apiErr); !v.OK() {
				return failf("agent did not answer within %s; see %s", agentWait, filepath.Join(config.LogDir(dataDir), logx.FileName))
			}
			return nil
		},
	}
	c.Flags().StringVar(&role, "role", "", "agent role: controller or node (required)")
	return c
}

func newServiceUninstallCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and unregister the service; the data dir is kept",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := requireAdmin(); err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			if checkNotInstalled(s) == nil {
				return failf("not installed")
			}
			_ = s.Stop()
			logEvent(dataDir, "service uninstalled", logx.EventServiceUninstalled)
			if err := s.Uninstall(); err != nil {
				return failf("uninstall service: %w", err)
			}
			fmt.Fprintf(c.OutOrStdout(), "uninstalled service %s; data kept in %s\n", svc.Name, dataDir)
			return nil
		},
	}
}

func newServiceStartCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start the service and wait for the agent",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := requireAdmin(); err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			if err := s.Start(); err != nil {
				return failf("start service: %w", err)
			}
			st, apiErr := waitForAgent(localapi.NewClient(localapi.Endpoint(dataDir)), agentWait)
			if !printStatus(c.OutOrStdout(), s, dataDir, st, apiErr).OK() {
				return errSilent1
			}
			return nil
		},
	}
}

func newServiceStopCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the service",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if err := requireAdmin(); err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			if err := s.Stop(); err != nil {
				return failf("stop service: %w", err)
			}
			fmt.Fprintln(c.OutOrStdout(), "stopped")
			return nil
		},
	}
}

func newServiceStatusCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the service state and whether the agent answers",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			st, apiErr := queryAgent(localapi.NewClient(localapi.Endpoint(dataDir)))
			if !printStatus(c.OutOrStdout(), s, dataDir, st, apiErr).OK() {
				return errSilent1
			}
			return nil
		},
	}
}
```

Register in `newRootCmd`: `root.AddCommand(newVersionCmd(), newAgentCmd(g), newServiceCmd(g))`.

- [ ] **Step 5: Run the tests to verify they pass; vet for Windows**

Run: `go test -race ./svc/ ./cmd/ && GOOS=windows go vet ./... && GOOS=linux go vet ./...`
Expected: PASS; vet clean.

- [ ] **Step 6: Commit**

```bash
git add svc/ cmd/ go.mod go.sum
git commit -m "feat(svc): service install/uninstall/start/stop/status with data dir, group and agent wait"
```

---

### Task 7: `sevjala doctor`

**Files:**
- Create: `cmd/doctor.go`, `cmd/doctor_test.go`
- Modify: `cmd/root.go` (register `newDoctorCmd(g)`)

**Interfaces:**
- Consumes: `buildStatusView`, `serviceStatuser`, `queryAgent`, `config.Load/Path`, `localapi.*`, `svc.New/IsAdmin`
- Produces: `type checkResult struct{Name, Detail string; OK bool}`; `checkDataDir(dir string) checkResult`; `checkConfig(dataDir string) checkResult`; `serviceChecks(v statusView, endpoint string) []checkResult`; `renderChecks(w io.Writer, rs []checkResult) bool` (true when all ok)

- [ ] **Step 1: Write the failing test** — `cmd/doctor_test.go`

```go
package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/config"
)

func TestCheckDataDir(t *testing.T) {
	dir := t.TempDir()
	if r := checkDataDir(dir); !r.OK {
		t.Errorf("writable dir: %+v", r)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("probe file left behind: %v", entries)
	}
	if r := checkDataDir(filepath.Join(dir, "missing")); r.OK {
		t.Errorf("missing dir passed: %+v", r)
	}
}

func TestCheckConfig(t *testing.T) {
	dir := t.TempDir()
	if r := checkConfig(dir); r.OK {
		t.Errorf("missing config passed: %+v", r)
	}
	config.Write(config.Path(dir), config.Default(config.RoleNode))
	if r := checkConfig(dir); !r.OK || r.Detail != "role=node" {
		t.Errorf("valid config: %+v", r)
	}
}

func TestServiceChecks(t *testing.T) {
	up := buildStatusView(fakeService{st: service.StatusRunning}, answering, nil, "/d", true)
	for _, r := range serviceChecks(up, "/d/sevjala.sock") {
		if !r.OK {
			t.Errorf("healthy view failed %s: %s", r.Name, r.Detail)
		}
	}
	down := buildStatusView(fakeService{err: service.ErrNotInstalled}, answering, errors.New("refused"), "/d", true)
	for _, r := range serviceChecks(down, "/d/sevjala.sock") {
		if r.OK {
			t.Errorf("broken view passed %s", r.Name)
		}
	}
}

func TestRenderChecks(t *testing.T) {
	var b bytes.Buffer
	ok := renderChecks(&b, []checkResult{
		{Name: "data dir", Detail: "/d", OK: true},
		{Name: "local api", Detail: "refused", OK: false},
	})
	want := "ok    data dir    /d\nFAIL  local api   refused\n"
	if ok || b.String() != want {
		t.Errorf("ok=%v\n%s\nwant\n%s", ok, b.String(), want)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./cmd/ -run 'Check|Render'`
Expected: FAIL — undefined: `checkDataDir`, …

- [ ] **Step 3: Implement** — `cmd/doctor.go`

```go
package cmd

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/svc"
)

type checkResult struct {
	Name, Detail string
	OK           bool
}

func checkDataDir(dir string) checkResult {
	f, err := os.CreateTemp(dir, ".doctor-*")
	if err != nil {
		return checkResult{Name: "data dir", Detail: fmt.Sprintf("%s not writable: %v", dir, err)}
	}
	f.Close()
	_ = os.Remove(f.Name())
	return checkResult{Name: "data dir", Detail: dir, OK: true}
}

func checkConfig(dataDir string) checkResult {
	cfg, err := config.Load(config.Path(dataDir))
	if err != nil {
		return checkResult{Name: "config", Detail: err.Error()}
	}
	return checkResult{Name: "config", Detail: "role=" + cfg.Role, OK: true}
}

func serviceChecks(v statusView, endpoint string) []checkResult {
	agent := checkResult{Name: "local api", Detail: fmt.Sprintf("%s: %v", endpoint, v.AgentErr)}
	if v.Agent != nil {
		agent = checkResult{Name: "local api", Detail: endpoint + " → " + v.Agent.Version, OK: true}
	}
	return []checkResult{
		{Name: "service", Detail: v.Service + " (" + v.Platform + ")", OK: v.ServiceUp},
		agent,
	}
}

func renderChecks(w io.Writer, rs []checkResult) bool {
	all := true
	for _, r := range rs {
		mark := "ok"
		if !r.OK {
			mark, all = "FAIL", false
		}
		fmt.Fprintf(w, "%-6s%-12s%s\n", mark, r.Name, r.Detail)
	}
	return all
}

func newDoctorCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the data dir, config, service and local API",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			s, err := svc.New(dataDir, &svc.Agent{})
			if err != nil {
				return fail(err)
			}
			endpoint := localapi.Endpoint(dataDir)
			st, apiErr := queryAgent(localapi.NewClient(endpoint))
			v := buildStatusView(s, st, apiErr, dataDir, svc.IsAdmin())
			rs := append([]checkResult{checkDataDir(dataDir), checkConfig(dataDir)}, serviceChecks(v, endpoint)...)
			if !renderChecks(c.OutOrStdout(), rs) {
				return errSilent1
			}
			return nil
		},
	}
}
```

Register: `root.AddCommand(newVersionCmd(), newAgentCmd(g), newServiceCmd(g), newDoctorCmd(g))`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test -race ./cmd/ && make lint`
Expected: PASS; `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add cmd/
git commit -m "feat(cmd): sevjala doctor — data dir, config, service and local API checks"
```

---

### Task 8: Build matrix — go-win7 SDK, Windows resources, GoReleaser

**Files:**
- Create: `scripts/fetch-go-win7.sh`, `scripts/go-win7.sha256`, `winres/winres.json`, `.goreleaser.yaml`
- Modify: `Makefile`, `.gitignore`, `go.mod` (tool directive)

**Interfaces:**
- Produces: `make build-all` → `dist/sevjala-{windows-amd64.exe,win7-amd64.exe,win7-386.exe,darwin-arm64,darwin-amd64,linux-amd64}` + `dist/checksums.txt`; `make build-win7`; `make release`.

- [ ] **Step 1: Pin go-winres as a tool**

Run: `go get -tool github.com/tc-hib/go-winres@v0.3.3`
Expected: `go.mod` gains `tool github.com/tc-hib/go-winres`.

- [ ] **Step 2: `winres/winres.json`**

```json
{
  "RT_MANIFEST": {
    "#1": {
      "0409": {
        "identity": {"name": "Sevjala.Agent", "version": ""},
        "description": "Sevjala — Database Backup & Replication",
        "minimum-os": "win7",
        "execution-level": "as invoker",
        "ui-access": false,
        "auto-elevate": false,
        "dpi-awareness": "system",
        "long-path-aware": true
      }
    }
  },
  "RT_VERSION": {
    "#1": {
      "0000": {
        "fixed": {"file_version": "0.0.0.0", "product_version": "0.0.0.0"},
        "info": {
          "0409": {
            "CompanyName": "Sevjala",
            "FileDescription": "Sevjala — Database Backup & Replication",
            "InternalName": "sevjala",
            "LegalCopyright": "Copyright (c) 2026 Jufianto Henri",
            "OriginalFilename": "sevjala.exe",
            "ProductName": "Sevjala"
          }
        }
      }
    }
  }
}
```

- [ ] **Step 3: `scripts/go-win7.sha256`** (values from the release's `.dgst` files, verified locally for darwin-arm64)

```
# XTLS/go-win7 patched-1.27.2
82e7d2470f28e67af317aaa8c6947e421906cda0064a1d58cf0eef1abf9e3ec9  go-for-win7-darwin-arm64.zip
faab266a5f657993f45c30e724ea49f2a947f431e4610b3da8136466bbe1edde  go-for-win7-darwin-amd64.zip
3d9123e1a2dffda763c04135bebbc825dddb202561b6f4004ea3a3795bd30f66  go-for-win7-linux-amd64.zip
35a1e8c569d89d052f792b6df41897435fb2f44b6d1ede7960c29fff9eb60a7c  go-for-win7-linux-arm64.zip
```

- [ ] **Step 4: `scripts/fetch-go-win7.sh`** (then `chmod +x`)

```bash
#!/usr/bin/env bash
# Fetch the XTLS/go-win7 patched Go SDK for the Windows 7 assets into
# .cache/go-win7, verified against scripts/go-win7.sha256.
set -euo pipefail

tag=patched-1.27.2
root=$(cd "$(dirname "$0")/.." && pwd)
dest="$root/.cache/go-win7"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
esac
zip="go-for-win7-$os-$arch.zip"

want=$(awk -v f="$zip" '$2 == f { print $1 }' "$root/scripts/go-win7.sha256")
if [ -z "$want" ]; then
  echo "fetch-go-win7: no checksum for $zip in scripts/go-win7.sha256" >&2
  exit 1
fi

if [ -x "$dest/bin/go" ] && [ "$(cat "$dest/.sha256" 2>/dev/null)" = "$want" ]; then
  exit 0
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$zip" "https://github.com/XTLS/go-win7/releases/download/$tag/$zip"

if command -v sha256sum >/dev/null; then
  got=$(sha256sum "$tmp/$zip" | awk '{ print $1 }')
else
  got=$(shasum -a 256 "$tmp/$zip" | awk '{ print $1 }')
fi
if [ "$got" != "$want" ]; then
  echo "fetch-go-win7: checksum mismatch for $zip: got $got, want $want" >&2
  exit 1
fi

rm -rf "$dest"
mkdir -p "$dest"
unzip -q "$tmp/$zip" -d "$dest"
echo "$want" > "$dest/.sha256"
GOTOOLCHAIN=local "$dest/bin/go" version
```

- [ ] **Step 5: `.goreleaser.yaml`**

```yaml
version: 2
project_name: sevjala

before:
  hooks:
    - scripts/fetch-go-win7.sh
    - go tool go-winres make --arch amd64,386 --product-version {{ .Version }} --file-version {{ .Major }}.{{ .Minor }}.{{ .Patch }}.0

builds:
  - id: stock
    binary: sevjala
    env: [CGO_ENABLED=0]
    flags: [-trimpath]
    ldflags: ["-s -w -X github.com/jufianto/sevjala/cmd.version={{ .Version }}"]
    goos: [windows, darwin, linux]
    goarch: [amd64, arm64]
    ignore:
      - {goos: windows, goarch: arm64}
      - {goos: linux, goarch: arm64}
  - id: win7
    binary: sevjala
    tool: .cache/go-win7/bin/go
    env: [CGO_ENABLED=0, GOTOOLCHAIN=local]
    flags: [-trimpath]
    ldflags: ["-s -w -X github.com/jufianto/sevjala/cmd.version={{ .Version }}"]
    goos: [windows]
    goarch: [amd64, "386"]

archives:
  - id: stock
    ids: [stock]
    formats: [binary]
    name_template: "sevjala-{{ .Os }}-{{ .Arch }}"
  - id: win7
    ids: [win7]
    formats: [binary]
    name_template: "sevjala-win7-{{ .Arch }}"

checksum:
  name_template: checksums.txt

snapshot:
  version_template: "{{ incpatch .Version }}-dev"

changelog:
  disable: true
```

- [ ] **Step 6: Makefile and .gitignore**

Append to `Makefile` and extend `.PHONY` to `build build-all build-win7 test lint release`:

```make
build-all:
	goreleaser build --snapshot --clean

build-win7:
	goreleaser build --snapshot --clean --id win7

release:
	goreleaser release --clean
```

Append to `.gitignore`:

```
# go-winres output, generated by GoReleaser's before-hook
*.syso
```

- [ ] **Step 7: Verify the whole matrix**

Run: `goreleaser check && make build-all && ls dist/ && file dist/*/sevjala* | sort`
Expected: `goreleaser check` passes; six binaries — windows/amd64 PE32+ (stock), windows/amd64 PE32+ and windows/386 PE32 (win7), darwin arm64/amd64 Mach-O, linux amd64 ELF; `dist/checksums.txt` lists the six asset names `sevjala-windows-amd64.exe`, `sevjala-win7-amd64.exe`, `sevjala-win7-386.exe`, `sevjala-darwin-arm64`, `sevjala-darwin-amd64`, `sevjala-linux-amd64`. If the names differ (e.g. `.exe` missing or an `_v1` suffix), fix `name_template` until they match exactly.

Run: `strings dist/win7_windows_386*/sevjala.exe | grep -c asInvoker`
Expected: `1` or more (manifest embedded).

Run: `rm -rf .cache/go-win7 && scripts/fetch-go-win7.sh && sed -i.bak 's/^82e7/0000/' scripts/go-win7.sha256 && rm -rf .cache/go-win7 && (scripts/fetch-go-win7.sh; echo "exit $?"); mv scripts/go-win7.sha256.bak scripts/go-win7.sha256`
Expected: first fetch prints `go version go1.27.2 darwin/arm64`; the tampered run prints `fetch-go-win7: no checksum for …` or `checksum mismatch` and `exit 1`; the sums file is restored.

- [ ] **Step 8: Commit**

```bash
git add scripts/ winres/ .goreleaser.yaml Makefile .gitignore go.mod go.sum
git commit -m "build: GoReleaser matrix with go-win7 assets, pinned SDK checksum, Windows manifest and version"
```

---

### Task 9: CI — test, lint, build, service smoke

**Files:**
- Create: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `make build-all`, `go test`, the CLI's exit codes.

- [ ] **Step 1: `.github/workflows/ci.yml`**

```yaml
name: ci

on:
  push:
    branches: [master]
    paths-ignore: ["**.md", "docs/**", "LICENSE"]
  pull_request:
    paths-ignore: ["**.md", "docs/**", "LICENSE"]

concurrency:
  group: ci-${{ github.ref }}
  cancel-in-progress: true

permissions:
  contents: read

jobs:
  test:
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: .tool-versions
      - name: test (race)
        if: runner.os != 'Windows'
        run: go test -race ./...
      - name: test
        if: runner.os == 'Windows'
        run: go test ./...

  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: .tool-versions
      - uses: golangci/golangci-lint-action@v8
        with:
          version: v2.14.0

  build:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0
      - uses: actions/setup-go@v5
        with:
          go-version-file: .tool-versions
      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: build --snapshot --clean
      - run: ls -l dist/ && cat dist/checksums.txt

  smoke:
    needs: test
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-latest, macos-latest, windows-latest]
    runs-on: ${{ matrix.os }}
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: .tool-versions
      - name: smoke (unix)
        if: runner.os != 'Windows'
        run: |
          set -x
          CGO_ENABLED=0 go build -o sevjala .
          sudo ./sevjala service install --role node
          sudo ./sevjala service status
          sudo ./sevjala doctor
          sudo ./sevjala service uninstall
          if sudo ./sevjala service status; then echo "status must fail after uninstall"; exit 1; fi
      - name: smoke (windows)
        if: runner.os == 'Windows'
        shell: pwsh
        run: |
          go build -o sevjala.exe .
          .\sevjala.exe service install --role node; if ($LASTEXITCODE) { exit 1 }
          .\sevjala.exe service status; if ($LASTEXITCODE) { exit 1 }
          .\sevjala.exe doctor; if ($LASTEXITCODE) { exit 1 }
          .\sevjala.exe service uninstall; if ($LASTEXITCODE) { exit 1 }
          .\sevjala.exe service status
          if ($LASTEXITCODE -eq 0) { throw "status must fail after uninstall" }
          exit 0
      - name: agent log on failure (unix)
        if: failure() && runner.os != 'Windows'
        run: sudo find /var/lib/sevjala "/Library/Application Support/Sevjala" -name 'sevjala.log*' -exec cat {} + 2>/dev/null || true
      - name: agent log on failure (windows)
        if: failure() && runner.os == 'Windows'
        shell: pwsh
        run: Get-Content "$env:ProgramData\Sevjala\logs\sevjala.log" -ErrorAction SilentlyContinue
```

- [ ] **Step 2: Validate locally**

Run: `go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/ci.yml`
Expected: no output, exit 0. (The real proof is the first push; see Task 10.)

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: test on 3 OSes, lint, build matrix with go-win7, service install smoke"
```

---

### Task 10: Docs, records, local smoke

**Files:**
- Modify: `docs/protocol.md`, `docs/PROGRESS.md`, `README.md`, `docs/superpowers/specs/2026-10-12-p0-foundation-design.md` (privileges line for `doctor`, Windows custom pipe DACL)

- [ ] **Step 1: `docs/protocol.md`** — append after the table:

````markdown
## Local API (implemented)

Transport: HTTP/1.1 over `\\.\pipe\sevjala` (Windows, SDDL `D:P(A;;GA;;;BA)(A;;GA;;;SY)`) or
`/var/run/sevjala.sock` (macOS/Linux, `0660 root:sevjala`). A non-default data dir uses
`<data-dir>/sevjala.sock` or `\\.\pipe\sevjala-<first 8 hex of sha256(data dir)>` (default DACL).

### `GET /status` (since P0)

`200 application/json`

```json
{"version": "0.1.0", "role": "node", "pid": 4120, "uptime_s": 133, "data_dir": "C:\\ProgramData\\Sevjala"}
```

| Field | Type | Meaning |
|---|---|---|
| `version` | string | agent build version (`dev` for local builds) |
| `role` | string | `controller` or `node` |
| `pid` | int | agent process id |
| `uptime_s` | int | seconds since the agent started |
| `data_dir` | string | absolute data dir |

Any other path: `404 {"error":"not found"}`.
````

- [ ] **Step 2: `docs/PROGRESS.md`** — P0 row becomes:

```markdown
| P0 Foundation | in progress — awaiting Win7 VM smoke | [spec](./superpowers/specs/2026-10-12-p0-foundation-design.md) · [plan](./superpowers/plans/2026-10-12-p0-foundation.md) | |
```

- [ ] **Step 3: `README.md`** — replace the Status paragraph with:

````markdown
**Status:** pre-alpha. P0 (service + local API) in progress, see [docs/PROGRESS.md](docs/PROGRESS.md).

## Try it

Put the binary in a permanent folder first; the service runs it from there.

```sh
sudo ./sevjala service install --role node   # Windows: Administrator prompt, no sudo
sudo ./sevjala service status
sudo ./sevjala doctor
sudo ./sevjala service uninstall             # keeps the data dir
```

Build: `make build` (host), `make build-all` (every asset incl. Windows 7), `make test`, `make lint`.
````

- [ ] **Step 4: Spec touch-ups** — in the spec's Status "Privileges" paragraph, replace the first sentence with: "`doctor` needs sudo / an Administrator prompt everywhere (it writes a probe file into the data dir); on Windows `status` does too (the pipe ACL allows only Administrators + SYSTEM)." In Runtime → Local endpoint, append to the non-default bullet: "On Windows the custom pipe keeps the default DACL so an un-elevated developer can reach their own agent." In D-008's row in `docs/decisions.md`, append the same Windows sentence.

- [ ] **Step 5: Full verification**

Run: `make lint test build-all`
Expected: lint `0 issues.`, all packages `ok`, six assets in `dist/`.

Run (owner, needs the sudo password — suggest typing it with `!`):
`! sudo ./sevjala service install --role node && ./sevjala service status; sudo ./sevjala doctor; sudo ./sevjala service uninstall`
Expected: install prints `wrote …/config.toml`, the group notice, `installed service sevjala → …`, then the status block with `running` + `answering`; doctor prints four `ok` lines; uninstall prints `data kept in /Library/Application Support/Sevjala`.

- [ ] **Step 6: Commit**

```bash
git add docs/ README.md
git commit -m "docs: local API GET /status in protocol.md, P0 progress, README try-it"
```

---

## After the tasks

1. Push `p0-foundation` and open the PR (owner approves the push); CI must be green on all jobs.
2. Owner runs the Windows 7 VM smoke from the spec and pastes the transcript into the PR.
3. Then PROGRESS.md: P0 `done` with the PR link, M1 `next`.
