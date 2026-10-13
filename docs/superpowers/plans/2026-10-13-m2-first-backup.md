# M2 First Backup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The controller dumps a configured MySQL/MariaDB database once (gzip + SHA-256 in one pass), keeps it in staging, and pushes it to every paired node, which stores it only after the hash and size match. The M1 pairing gap (I-5) is closed first.

**Architecture:**
- **Node:** `inventory` owns the node store (receive, scan, partial-file cleanup).
- **Controller:**
  - `dump` finds the tools, detects databases through the `mysql` client binary, and streams `mysqldump`.
  - `store` v2 records backups and per-node copies.
  - `transfer` is a per-node worker pool with whole-file retries.
- **LAN:** `lanapi` adds `PUT /backups/{id}` and a full `/status`.
- **Glue:** `coord` wires it all behind the local API, and `cmd` adds `db detect|set`, `backup now` and `backups list`.

**Tech Stack:**
- Go 1.27, standard library only for the work: `compress/gzip`, `crypto/sha256`, `os/exec`, `net/http`.
- `modernc.org/sqlite`.
- New: `github.com/zalando/go-keyring` (named in the blueprint) and `golang.org/x/term`.

**Spec:** `docs/superpowers/specs/2026-10-13-m2-first-backup-design.md`

## Global Constraints

- **Toolchain and dependencies:**
  - Go: `export PATH="$HOME/.asdf/shims:$PATH"` before any `go` command. `go.mod` stays `go 1.27.0`.
  - Pure Go, `CGO_ENABLED=0` for the agent, the CLI and every Windows asset. Everything must also compile with the go-win7 SDK of the same minor.
  - New dependencies: only `github.com/zalando/go-keyring` and `golang.org/x/term`, each with a `docs/decisions.md` row.
- **Layout and style:**
  - Packages live at the repo root, no `internal/` (D-001). New dirs: `inventory/`, `dump/`, `dump/dumptest/`, `transfer/`.
  - Logs are `log/slog` with an `event` field (`logx.Event(...)`). Errors wrap with `%w`. No global state beyond the service entry point.
  - Default is no comment; comments only for a non-obvious why. No issue numbers in code.
  - Tests are table-driven where there are several cases; anything touching the LAN or local API gets a loopback test.
- **Secrets:**
  - The DB password never appears in config files, logs, `/status`, error messages, argv or test fixtures.
  - It reaches child processes only via `MYSQL_PWD`.
- **Non-negotiables:**
  - Nodes never initiate LAN connections.
  - No backup is reported `verified` before hash match and atomic rename.
  - Nodes never delete a stored backup; they delete only their own `.part` files.
- **Formats:** backup ID `BK-<yyyymmdd>-<hhmmss>-<4 uppercase hex>`. Manifest `v` = 1.
- **Verify order:** `go vet ./...` (also `GOOS=windows` and `GOOS=linux`) → `golangci-lint run` → `go test -race ./...` → `make build-all`.
- **Commits:** atomic, one per task unless a task says otherwise. Never push.

## Review Focus

1. **A password with spaces, quotes, `$` or non-ASCII characters** must reach `mysqldump` byte-exact through `MYSQL_PWD` and be scrubbed from error text (test in Task 4).
2. **A node re-paired to another controller** refuses the push at TLS. The copy must end `failed` after one attempt, not retry for 12 minutes (test in Task 9).
3. **A controller restart mid-transfer** must requeue the copy and verify it. Unfinished staging dumps are deleted at start (tests in Tasks 9 and 11).
4. **A retry after a lost response** (the node stored the backup, the controller never saw the 200) must succeed without resending the body (test in Task 7).
5. **A body longer than `X-Sevjala-Size`** must be a mismatch, never silently truncated into a `verified` file (test in Task 3).

---

### Task 1: Pairing fingerprint check (I-5)

**Files:**
- Create: `pairing/fp.go`, `pairing/fp_test.go`
- Modify: `coord/controller.go` (`Pair`), `coord/coord.go` (`Service.Pair`, `Node`), `coord/integration_test.go`, `localapi/api.go` (`Code`, `Backend.Pair`), `localapi/server.go` (`POST /pair`), `localapi/client.go` (`Pair`), `localapi/localapi_test.go`, `cmd/coord.go` (`renderCode`, `newPairCmd`), `cmd/coord_test.go`, `svc/agent.go` (`coord.Node{FP}`), `logx` unchanged

**Interfaces:**
- Produces:
  - `pairing.FPChars = 16`
  - `pairing.ShortFP(fp string) string`
  - `pairing.ParseFP(s string) (string, bool)`
  - `pairing.MatchFP(fp, prefix string) bool`
  - `localapi.Code.FP string` (json `fp`)
  - `Backend.Pair(ctx, agent, code, fp string) (Agent, error)`
  - `Client.Pair(ctx, agent, code, fp string)`
  - `coord.Node.FP string`

- [ ] **Step 1: Write the failing pairing test**

`pairing/fp_test.go`:

```go
package pairing

import "testing"

func TestParseFP(t *testing.T) {
	tests := []struct {
		in, want string
		ok       bool
	}{
		{"3f2a9b1c77de0a41", "3f2a9b1c77de0a41", true},
		{"sha256:3F2A9B1C77DE0A41", "3f2a9b1c77de0a41", true},
		{" 3f2a-9b1c 77de:0a41 ", "3f2a9b1c77de0a41", true},
		{"3f2a9b1c77de0a41ff", "3f2a9b1c77de0a41ff", true},
		{"3f2a9b1c77de0a4", "", false},
		{"3f2a9b1c77de0a4g", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := ParseFP(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("ParseFP(%q) = %q, %v; want %q, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestShortAndMatchFP(t *testing.T) {
	fp := "sha256:3f2a9b1c77de0a41aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if got := ShortFP(fp); got != "3f2a9b1c77de0a41" {
		t.Errorf("ShortFP = %q", got)
	}
	if !MatchFP(fp, "3f2a9b1c77de0a41") || MatchFP(fp, "0000000000000000") {
		t.Error("MatchFP")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./pairing/ -run 'FP'`
Expected: FAIL, `undefined: ParseFP`.

- [ ] **Step 3: Implement `pairing/fp.go`**

```go
package pairing

import "strings"

// FPChars is how much of its fingerprint a node shows next to the code: 64
// bits, out of reach for an impostor generating certificates.
const FPChars = 16

// ShortFP is the part of a "sha256:<hex>" fingerprint that 'sevjala code' shows.
func ShortFP(fp string) string {
	h := strings.TrimPrefix(fp, "sha256:")
	if len(h) > FPChars {
		h = h[:FPChars]
	}
	return h
}

// ParseFP normalises what the operator typed: an optional "sha256:", spaces,
// dashes and colons dropped, any case; at least FPChars hex characters.
func ParseFP(s string) (string, bool) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "sha256:")
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' || r == ':' {
			return -1
		}
		return r
	}, s)
	if len(s) < FPChars || len(s) > 64 || strings.Trim(s, "0123456789abcdef") != "" {
		return "", false
	}
	return s, true
}

// MatchFP reports whether fingerprint fp starts with a ParseFP result.
func MatchFP(fp, prefix string) bool {
	return strings.HasPrefix(strings.TrimPrefix(fp, "sha256:"), prefix)
}
```

Run: `go test ./pairing/` → Expected: PASS.

- [ ] **Step 4: Write the failing coord test**

Update every `ctrl.Pair(ctx, X, code)` in `coord/integration_test.go` to pass the node's fingerprint as a fourth argument:
- `TestDiscoverPairPing`, `TestAddAgentThenPair`, `TestDiscoverRefreshesPairedAddress` and `TestAddAgentRefreshesPairedAddress` pass `node.id.FP`.
- `TestPairWrongCode` and `TestPairWithoutActiveCodeSaysWhy` pass `node.id.FP`.
- `TestPairRefusesController` passes `other.id.FP`.
- `TestPairNeedsDiscovery` and `TestPairRejectsBadCode` pass `strings.Repeat("a", 16)`.

Then add:

```go
// The fingerprint shown next to the code is checked before anything is sent,
// so a device answering discovery for the node never sees a proof.
func TestPairChecksFingerprint(t *testing.T) {
	ctx := context.Background()
	node := startAgent(t, "node", "PC-FP")
	ctrl := newController(t, node.disco)
	ctrl.Discover(ctx)
	code, _, _ := node.codes.Issue()
	if _, err := ctrl.Pair(ctx, "PC-FP", code, "0000000000000000"); err == nil || !strings.Contains(err.Error(), "do not pair") {
		t.Fatalf("wrong fp: %v", err)
	}
	if _, err := ctrl.Pair(ctx, "PC-FP", code, "abc"); err == nil || !strings.Contains(err.Error(), "16 hex") {
		t.Fatalf("short fp: %v", err)
	}
	if _, err := ctrl.Pair(ctx, "PC-FP", code, "SHA256:"+strings.ToUpper(pairing.ShortFP(node.id.FP))); err != nil {
		t.Fatalf("pair after refusals: %v", err)
	}
}
```

Run: `go test ./coord/` → Expected: FAIL to compile (`too many arguments in call to ctrl.Pair`).

- [ ] **Step 5: Implement the coord change**

In `coord/controller.go`, change `Pair`:

```go
func (c *Controller) Pair(ctx context.Context, agent, code, fp string) (localapi.Agent, error) {
	if !pairing.ValidCode(code) {
		return localapi.Agent{}, localapi.Errorf(http.StatusBadRequest, "the code must be the 6 digits shown by 'sevjala code' on the node")
	}
	prefix, ok := pairing.ParseFP(fp)
	if !ok {
		return localapi.Agent{}, localapi.Errorf(http.StatusBadRequest,
			"the fingerprint must be the %d hex characters shown by 'sevjala code' on the node", pairing.FPChars)
	}
	c.mu.Lock()
	seen := slices.Collect(maps.Values(c.seen))
	c.mu.Unlock()
	d, err := resolve(seen, agent, func(d localapi.Discovered) (string, string) { return d.AgentID, d.Hostname }, seenHint)
	if err != nil {
		return localapi.Agent{}, err
	}
	if d.Role != config.RoleNode {
		return localapi.Agent{}, localapi.Errorf(http.StatusConflict, "%s is a %s; only nodes can be paired", d.Hostname, d.Role)
	}
	if !pairing.MatchFP(d.FP, prefix) {
		c.Log.Warn("pairing refused", logx.Event(logx.EventPairRejected), "agent_id", d.AgentID, "addr", d.Addr,
			"reason", "fingerprint mismatch")
		return localapi.Agent{}, localapi.Errorf(http.StatusConflict,
			"the agent found at %s has fingerprint %s, not the one shown by 'sevjala code'; do not pair: another device may be answering for it",
			d.Addr, pairing.ShortFP(d.FP))
	}
	// … the rest of the function is unchanged (LAN.Pair, store, remember, log)
```

In `coord/coord.go`:

```go
func (s *Service) Pair(ctx context.Context, agent, code, fp string) (localapi.Agent, error) {
	if s.Controller == nil {
		return localapi.Agent{}, errNotController
	}
	return s.Controller.Pair(ctx, agent, code, fp)
}

type Node struct {
	AgentID string
	FP      string
	Codes   *pairing.Codes
	Log     *slog.Logger
}

func (n *Node) Code() (localapi.Code, error) {
	code, exp, err := n.Codes.Issue()
	if err != nil {
		return localapi.Code{}, err
	}
	n.Log.Info("pairing code issued", logx.Event(logx.EventPairCodeIssued), "expires_at", exp)
	return localapi.Code{Code: code, ExpiresAt: exp, AgentID: n.AgentID, FP: n.FP}, nil
}
```

Changes in `localapi`:
- **`api.go`:** add `FP string \`json:"fp"\`` to `Code`. The `Backend` line becomes `Pair(ctx context.Context, agent, code, fp string) (Agent, error)`.
- **`server.go` `POST /pair`:** the request struct gains `FP string \`json:"fp"\``, and the call becomes `b.Pair(r.Context(), req.Agent, req.Code, req.FP)`.
- **`client.go`:**

  ```go
  func (c *Client) Pair(ctx context.Context, agent, code, fp string) (Agent, error) {
  	var a Agent
  	return a, c.do(ctx, http.MethodPost, "/pair", map[string]string{"agent": agent, "code": code, "fp": fp}, &a)
  }
  ```

- **`localapi_test.go`:**
  - `fakeBackend` gains `gotFP string`.
  - `Pair(_ context.Context, agent, code, fp string)` records `f.gotFP = fp`.
  - The pair call in the round-trip test becomes `c.Pair(ctx, "3f2a", "654321", "3f2a9b1c77de0a41")`, and it asserts `f.gotFP == "3f2a9b1c77de0a41"`.

In `svc/agent.go`: `backend.Node = &coord.Node{AgentID: id.AgentID, FP: id.FP, Codes: codes, Log: a.log}`.

- [ ] **Step 6: Write the failing CLI test, then implement**

`cmd/coord_test.go` `TestRenderCode`:

```go
func TestRenderCode(t *testing.T) {
	var b bytes.Buffer
	renderCode(&b, localapi.Code{Code: "123456", AgentID: "3f2a9c1e-1111", FP: "sha256:3f2a9b1c77de0a41ffff",
		ExpiresAt: time.Date(2026, 10, 12, 16, 42, 0, 0, time.Local)})
	want := "pairing code 123456, valid until 16:42\nfingerprint  3f2a9b1c77de0a41\n" +
		"on the controller run: sevjala pair 3f2a9c1e --code 123456 --fp 3f2a9b1c77de0a41\n"
	if b.String() != want {
		t.Errorf("got %q", b.String())
	}
}
```

`TestCoordUsageErrors` gets two new cases:
- `{"pair", "x", "--code", "123456"}` → exit 2, stderr containing `--fp`.
- `{"pair", "x", "--code", "123456", "--fp", "abc"}` → exit 2.

Follow the table shape already in that test.

Run: `go test ./cmd/` → FAIL. Then in `cmd/coord.go`:

```go
func renderCode(w io.Writer, c localapi.Code) {
	fp := pairing.ShortFP(c.FP)
	fmt.Fprintf(w, "pairing code %s, valid until %s\n", c.Code, c.ExpiresAt.Local().Format("15:04"))
	fmt.Fprintf(w, "fingerprint  %s\n", fp)
	fmt.Fprintf(w, "on the controller run: sevjala pair %s --code %s --fp %s\n", shortID(c.AgentID), c.Code, fp)
}
```

`newPairCmd` gains `var fp string`:
- Use: `"pair <agent> --code <code> --fp <fingerprint>"`.
- After the code check:

  ```go
  			if _, ok := pairing.ParseFP(fp); !ok {
  				return usagef("--fp must be the %d-character fingerprint shown by 'sevjala code' on the node", pairing.FPChars)
  			}
  ```

- The call becomes `cl.Pair(ctx, args[0], code, fp)`.
- Flag: `c.Flags().StringVar(&fp, "fp", "", "fingerprint from 'sevjala code' on the node")`.

- [ ] **Step 7: Run everything touched**

Run: `go test -race ./pairing/ ./coord/ ./localapi/ ./cmd/ ./svc/`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add pairing coord localapi cmd svc
git commit -m "fix(pairing): pair checks the fingerprint shown by 'sevjala code' before sending the proof"
```

---

### Task 2: Config database defaults, full write, secrets

**Files:**
- Modify: `config/config.go`, `config/paths.go`, `config/config_test.go`, `go.mod`, `go.sum`
- Create: `config/secret.go`, `config/secret_test.go`

**Interfaces:**
- Produces:
  - Constants:
    - `config.DefaultDBHost = "127.0.0.1"`
    - `DefaultDBPort = 3306`
    - `DefaultDBUser = "root"`
    - `DefaultMinFreeBytes int64 = 2 << 30`
  - `Config.StorePath(dataDir string) string`
  - `config.StagingDir(dataDir string) string`
  - `config.Write(path, Config) error` (whole file, atomic)
  - `config.KeyringPrefix = "keyring:"`
  - `config.ErrNoSecret`
  - `config.SetSecret(dataDir, name, value string) (fallback bool, err error)`
  - `config.Secret(dataDir, ref string) (string, error)`
  - `config.DeleteSecret(dataDir, name string) error`

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/zalando/go-keyring@v0.2.8`
Expected: `go.mod` lists `github.com/zalando/go-keyring v0.2.8` (plus indirect `danieljoos/wincred`, `godbus/dbus/v5`).

- [ ] **Step 2: Write the failing config tests**

Append to `config/config_test.go`:

```go
func TestDefaultsForM2(t *testing.T) {
	c := Default(RoleController)
	if c.Controller.Database.Host != "127.0.0.1" || c.Controller.Database.Port != 3306 || c.Controller.Database.User != "root" {
		t.Errorf("database defaults %+v", c.Controller.Database)
	}
	if c.Node.Store.MinFreeBytes != 2<<30 {
		t.Errorf("min_free_bytes %d", c.Node.Store.MinFreeBytes)
	}
	if got := c.StorePath("/data"); got != filepath.Join("/data", "store") {
		t.Errorf("StorePath = %q", got)
	}
	c.Node.Store.Path = "/backups"
	if got := c.StorePath("/data"); got != "/backups" {
		t.Errorf("StorePath = %q", got)
	}
}

func TestWriteFullRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	c := Default(RoleController)
	c.Controller.Database.PasswordRef = "keyring:db"
	c.Controller.Database.Dumper = `C:\xampp\mysql\bin\mysqldump.exe`
	c.Controller.Database.Databases = []string{"shop", "legacy"}
	c.Controller.Staging.Keep = 12
	if err := Write(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Errorf("round trip\n got %+v\nwant %+v", got, c)
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("temp file left: %v", err)
	}
}

func TestValidateDatabasePort(t *testing.T) {
	_, err := Load(writeFile(t, "role = \"controller\"\n[controller.database]\nport = 0"))
	if err == nil || !strings.Contains(err.Error(), "controller.database.port") {
		t.Errorf("err = %v", err)
	}
}
```

Add `reflect` to the imports. Update the `blueprint example` case's `want` so `Database` keeps the same values; it already sets them. `Load` starts from `Default("")`, so the minimal-controller case still equals `Default(RoleController)`.

Run: `go test ./config/` → Expected: FAIL (`c.StorePath undefined`).

- [ ] **Step 3: Implement the config changes**

In `config/config.go`, add to the const block:

```go
	DefaultDBHost              = "127.0.0.1"
	DefaultDBPort              = 3306
	DefaultDBUser              = "root"
	DefaultMinFreeBytes  int64 = 2 << 30
```

Replace `Default`, extend `Validate`, and replace `Write`:

```go
func Default(role string) Config {
	return Config{
		Role: role, Listen: DefaultListen, DiscoveryPort: DefaultDiscoveryPort,
		Controller: Controller{Database: Database{Host: DefaultDBHost, Port: DefaultDBPort, User: DefaultDBUser}},
		Node:       Node{Store: Store{MinFreeBytes: DefaultMinFreeBytes}},
	}
}
```

In `Validate`, before `return nil`:

```go
	if p := c.Controller.Database.Port; p < 1 || p > 65535 {
		return fmt.Errorf("controller.database.port %d: must be 1-65535", p)
	}
	if c.Node.Store.MinFreeBytes < 0 {
		return fmt.Errorf("node.store.min_free_bytes %d: must not be negative", c.Node.Store.MinFreeBytes)
	}
```

```go
// StorePath is where a node keeps backups: [node.store] path, else
// <data-dir>/store, which inherits the locked data dir ACL.
func (c Config) StorePath(dataDir string) string {
	if c.Node.Store.Path != "" {
		return c.Node.Store.Path
	}
	return filepath.Join(dataDir, "store")
}

// Write replaces the whole file atomically; the agent is the only writer.
func Write(path string, c Config) error {
	var b bytes.Buffer
	if err := toml.NewEncoder(&b).Encode(c); err != nil {
		return err
	}
	return writeFile(path, b.Bytes(), 0o644)
}

// writeFile goes through a synced temp file and a rename, so a crash leaves
// the old file or the complete new one.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
```

Imports gain `bytes` and `path/filepath`. In `config/paths.go`, add:

```go
func StagingDir(dataDir string) string { return filepath.Join(dataDir, "staging") }
```

Run: `go test ./config/` → Expected: PASS. If `TestWriteFullRoundTrip` fails on a nil vs empty slice, keep `Databases` non-nil in the test; never weaken the comparison. The existing `Write` round-trip test (config_test.go, `Write(path, Default(RoleNode))`) must still pass. `svc/install.go` keeps calling `config.Write(path, config.Default(role))`, so a fresh install now writes every default, the `[controller]` and `[node]` tables included. That is intended: the operator sees every knob.

- [ ] **Step 4: Write the failing secret tests**

`config/secret_test.go`:

```go
package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestSecretRoundTrip(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	if fb, err := SetSecret(dir, "db", "p a$s\"wörd"); err != nil || fb {
		t.Fatalf("SetSecret = %v, %v", fb, err)
	}
	if got, err := Secret(dir, "keyring:db"); err != nil || got != "p a$s\"wörd" {
		t.Fatalf("Secret = %q, %v", got, err)
	}
	if err := DeleteSecret(dir, "db"); err != nil {
		t.Fatal(err)
	}
	if _, err := Secret(dir, "keyring:db"); !errors.Is(err, ErrNoSecret) {
		t.Errorf("after delete: %v", err)
	}
	if err := DeleteSecret(dir, "db"); err != nil {
		t.Errorf("second delete: %v", err)
	}
}

func TestSecretRefs(t *testing.T) {
	keyring.MockInit()
	dir := t.TempDir()
	if got, err := Secret(dir, ""); got != "" || err != nil {
		t.Errorf("empty ref = %q, %v", got, err)
	}
	if _, err := Secret(dir, "plain:db"); err == nil {
		t.Error("a ref without the keyring: prefix was accepted")
	}
}

// The Linux fallback, exercised directly so every OS runs it.
func TestSecretFile(t *testing.T) {
	dir := t.TempDir()
	if err := writeSecretFile(dir, "db", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if got, err := readSecretFile(dir, "db"); err != nil || got != "s3cret" {
		t.Fatalf("read = %q, %v", got, err)
	}
	raw, _ := os.ReadFile(secretFile(dir, "db"))
	if bytes.Contains(raw, []byte("s3cret")) {
		t.Error("secret stored in clear")
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(filepath.Join(dir, "secrets", "key")); fi.Mode().Perm() != 0o600 {
			t.Errorf("key mode %v", fi.Mode().Perm())
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "secrets", "key"), bytes.Repeat([]byte{7}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSecretFile(dir, "db"); err == nil {
		t.Error("decrypted with another key")
	}
}

func TestSetSecretFallsBackOnLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the file fallback is Linux only")
	}
	keyring.MockInitWithError(errors.New("no D-Bus session"))
	dir := t.TempDir()
	fb, err := SetSecret(dir, "db", "s3cret")
	if err != nil || !fb {
		t.Fatalf("SetSecret = %v, %v", fb, err)
	}
	if got, err := Secret(dir, "keyring:db"); err != nil || got != "s3cret" {
		t.Errorf("Secret = %q, %v", got, err)
	}
}
```

Run: `go test ./config/` → Expected: FAIL (`undefined: SetSecret`).

- [ ] **Step 5: Implement `config/secret.go`**

```go
package config

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	KeyringPrefix  = "keyring:"
	keyringService = "sevjala"
)

var ErrNoSecret = errors.New("secret not found")

// SetSecret stores value in the OS keyring. Linux without a Secret Service
// (a systemd service has no D-Bus session) falls back to an AES-GCM file
// keyed from a root-only key file, and reports fallback so the caller warns.
func SetSecret(dataDir, name, value string) (fallback bool, err error) {
	kerr := keyring.Set(keyringService, name, value)
	if kerr == nil {
		_ = os.Remove(secretFile(dataDir, name))
		return false, nil
	}
	if runtime.GOOS != "linux" {
		return false, fmt.Errorf("keyring: %w", kerr)
	}
	if err := writeSecretFile(dataDir, name, value); err != nil {
		return true, errors.Join(fmt.Errorf("keyring: %w", kerr), err)
	}
	return true, nil
}

// Secret resolves a password_ref; an empty ref is an empty password.
func Secret(dataDir, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	name, ok := strings.CutPrefix(ref, KeyringPrefix)
	if !ok || name == "" {
		return "", fmt.Errorf("password_ref %q: must be %q followed by a name", ref, KeyringPrefix)
	}
	v, kerr := keyring.Get(keyringService, name)
	if kerr == nil {
		return v, nil
	}
	v, ferr := readSecretFile(dataDir, name)
	switch {
	case ferr == nil:
		return v, nil
	case !errors.Is(ferr, fs.ErrNotExist):
		return "", ferr
	case errors.Is(kerr, keyring.ErrNotFound):
		return "", fmt.Errorf("%s: %w", ref, ErrNoSecret)
	}
	return "", fmt.Errorf("keyring: %w", kerr)
}

func DeleteSecret(dataDir, name string) error {
	kerr := keyring.Delete(keyringService, name)
	if errors.Is(kerr, keyring.ErrNotFound) || runtime.GOOS == "linux" {
		kerr = nil
	}
	ferr := os.Remove(secretFile(dataDir, name))
	if errors.Is(ferr, fs.ErrNotExist) {
		ferr = nil
	}
	return errors.Join(kerr, ferr)
}

func secretDir(dataDir string) string { return filepath.Join(dataDir, "secrets") }

func secretFile(dataDir, name string) string { return filepath.Join(secretDir(dataDir), name+".enc") }

func fileKey(dataDir string) ([]byte, error) {
	path := filepath.Join(secretDir(dataDir), "key")
	key, err := os.ReadFile(path)
	if err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("%s: want 32 bytes, got %d", path, len(key))
		}
		return key, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	_, _ = rand.Read(key)
	if err := os.MkdirAll(secretDir(dataDir), 0o700); err != nil {
		return nil, err
	}
	return key, writeFile(path, key, 0o600)
}

func aead(dataDir string) (cipher.AEAD, error) {
	key, err := fileKey(dataDir)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func writeSecretFile(dataDir, name, value string) error {
	a, err := aead(dataDir)
	if err != nil {
		return err
	}
	nonce := make([]byte, a.NonceSize())
	_, _ = rand.Read(nonce)
	return writeFile(secretFile(dataDir, name), a.Seal(nonce, nonce, []byte(value), []byte(name)), 0o600)
}

func readSecretFile(dataDir, name string) (string, error) {
	path := secretFile(dataDir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	a, err := aead(dataDir)
	if err != nil {
		return "", err
	}
	if len(raw) < a.NonceSize() {
		return "", fmt.Errorf("%s: truncated", path)
	}
	plain, err := a.Open(nil, raw[:a.NonceSize()], raw[a.NonceSize():], []byte(name))
	if err != nil {
		return "", fmt.Errorf("%s: cannot decrypt (was secrets/key replaced?)", path)
	}
	return string(plain), nil
}
```

Run: `go test -race ./config/` → Expected: PASS (the Linux fallback test is skipped on macOS).

- [ ] **Step 6: Check the Windows and Linux builds, then commit**

Run: `GOOS=windows go vet ./config/ && GOOS=linux go vet ./config/` → Expected: no output.

```bash
git add go.mod go.sum config
git commit -m "feat(config): database defaults, whole-file atomic write, keyring secrets with a Linux file fallback"
```

---

### Task 3: Node store (`inventory`)

**Files:**
- Create: `inventory/manifest.go`, `inventory/store.go`, `inventory/diskfree_unix.go`, `inventory/diskfree_windows.go`, `inventory/inventory_test.go`
- Modify: `logx/logx.go` (event `part_removed`)

**Interfaces:**
- Produces:
  - Constants:
    - `inventory.DataExt = ".sql.gz"`
    - `ManifestExt = ".json"`
    - `PartExt = ".part"`
    - `StateVerified = "verified"`
    - `StateCorrupt = "corrupt"`
    - `StalePart = time.Hour`
  - `type Manifest struct{V int; BackupID, Database string; CreatedAt time.Time; SourceAgent, ServerVersion string; DumpFlags []string; Size int64; SHA256 string; Tags []string}` with JSON tags `v, backup_id, database, created_at, source_agent, server_version, dump_flags, size, sha256, tags`.
  - `ValidID(string) bool`, `NewID(time.Time) string`, `(Manifest).Validate() error`.
  - `ReadManifest(path) (Manifest, error)`, `WriteManifest(path, Manifest) error`.
  - `WriteFile(path string, data []byte, mode os.FileMode) error`, `SyncDir(dir) error`.
  - `FreeBytes(dir) (int64, error)`.
  - `type Store`, with `Open(dir string, minFree int64, log *slog.Logger) (*Store, error)` and methods:
    - `(*Store).Dir() string`
    - `(*Store).FreeBytes() (int64, error)`
    - `(*Store).Receive(m Manifest, sha string, size int64, body io.Reader) error`
    - `(*Store).Scan() (Inventory, error)`
  - `type Copy struct{BackupID string; Size int64; SHA256, State string}`.
  - `type Inventory struct{Backups []Copy; Problems []string}`.
  - Errors: `ErrInvalid`, `ErrExists`, `ErrConflict`, `ErrMismatch`, `ErrNoSpace`.
  - `logx.EventPartRemoved = "part_removed"`.

- [ ] **Step 1: Write the failing tests**

`inventory/inventory_test.go`:

```go
package inventory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var discard = slog.New(slog.DiscardHandler)

const testID = "BK-20261013-101500-A8F2"

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir(), 0, discard)
	if err != nil {
		t.Fatal(err)
	}
	s.free = func(string) (int64, error) { return 1 << 40, nil }
	return s
}

func manifestFor(id string, data []byte) Manifest {
	sum := sha256.Sum256(data)
	return Manifest{V: 1, BackupID: id, Database: "shop", CreatedAt: time.Date(2026, 10, 13, 10, 15, 0, 0, time.UTC),
		SourceAgent: "c960acf5", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:]), Tags: []string{"manual"}}
}

func receive(s *Store, m Manifest, body []byte) error {
	return s.Receive(m, m.SHA256, m.Size, bytes.NewReader(body))
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestReceiveStoresFileAndManifest(t *testing.T) {
	s := newStore(t)
	data := []byte("-- dump\nINSERT INTO t VALUES (1);\n")
	m := manifestFor(testID, data)
	if err := receive(s, m, data); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(s.Dir(), testID+DataExt))
	if !bytes.Equal(got, data) {
		t.Errorf("stored %q", got)
	}
	gm, err := ReadManifest(filepath.Join(s.Dir(), testID+ManifestExt))
	if err != nil || gm.SHA256 != m.SHA256 || gm.Size != m.Size || !gm.CreatedAt.Equal(m.CreatedAt) {
		t.Errorf("manifest %+v, %v", gm, err)
	}
	if n := names(t, s.Dir()); len(n) != 2 {
		t.Errorf("files %v", n)
	}
	inv, err := s.Scan()
	if err != nil || len(inv.Backups) != 1 || inv.Backups[0].State != StateVerified || len(inv.Problems) != 0 {
		t.Errorf("scan %+v, %v", inv, err)
	}
}

func TestReceiveRejectsAndLeavesNothing(t *testing.T) {
	data := []byte("0123456789abcdef")
	tests := []struct {
		name string
		m    func(Manifest) Manifest
		sha  func(Manifest) string
		body []byte
		want error
	}{
		{name: "hash mismatch", body: []byte("0123456789abcdeX"), want: ErrMismatch},
		{name: "short body", body: data[:10], want: ErrMismatch},
		{name: "body longer than the size", body: append(append([]byte{}, data...), 'x'), want: ErrMismatch},
		{name: "path traversal id", m: func(m Manifest) Manifest { m.BackupID = "../x"; return m }, body: data, want: ErrInvalid},
		{name: "lower-case id", m: func(m Manifest) Manifest { m.BackupID = "BK-20261013-101500-a8f2"; return m }, body: data, want: ErrInvalid},
		{name: "headers differ from manifest", sha: func(Manifest) string { return strings.Repeat("0", 64) }, body: data, want: ErrInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newStore(t)
			m := manifestFor(testID, data)
			if tt.m != nil {
				m = tt.m(m)
			}
			sha := m.SHA256
			if tt.sha != nil {
				sha = tt.sha(m)
			}
			if err := s.Receive(m, sha, m.Size, bytes.NewReader(tt.body)); !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if n := names(t, s.Dir()); len(n) != 0 {
				t.Errorf("left %v", n)
			}
		})
	}
}

type failAfter struct{ r io.Reader }

func (f failAfter) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if err == io.EOF {
		return n, errors.New("connection reset")
	}
	return n, err
}

func TestReceiveAbortedBodyLeavesNothing(t *testing.T) {
	s := newStore(t)
	data := bytes.Repeat([]byte("x"), 1<<20)
	m := manifestFor(testID, data)
	if err := s.Receive(m, m.SHA256, m.Size, failAfter{bytes.NewReader(data[:1000])}); err == nil {
		t.Fatal("aborted body accepted")
	}
	if n := names(t, s.Dir()); len(n) != 0 {
		t.Errorf("left %v", n)
	}
}

type mustNotRead struct{ t *testing.T }

func (r mustNotRead) Read([]byte) (int, error) { r.t.Error("body read"); return 0, io.EOF }

func TestReceiveExisting(t *testing.T) {
	s := newStore(t)
	data := []byte("same backup")
	m := manifestFor(testID, data)
	if err := receive(s, m, data); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(m, m.SHA256, m.Size, mustNotRead{t}); !errors.Is(err, ErrExists) {
		t.Errorf("same hash: %v", err)
	}
	other := manifestFor(testID, []byte("other backup"))
	if err := receive(s, other, []byte("other backup")); !errors.Is(err, ErrConflict) {
		t.Errorf("other hash: %v", err)
	}
}

func TestReceiveInFlightIsConflict(t *testing.T) {
	s := newStore(t)
	if !s.claim(testID) {
		t.Fatal("claim")
	}
	data := []byte("x")
	if err := receive(s, manifestFor(testID, data), data); !errors.Is(err, ErrConflict) {
		t.Errorf("err = %v", err)
	}
}

func TestReceiveNoSpace(t *testing.T) {
	s := newStore(t)
	s.minFree = 50
	s.free = func(string) (int64, error) { return 100, nil }
	data := bytes.Repeat([]byte("x"), 60)
	if err := receive(s, manifestFor(testID, data), data); !errors.Is(err, ErrNoSpace) {
		t.Errorf("err = %v", err)
	}
}

func TestScanStates(t *testing.T) {
	s := newStore(t)
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(s.Dir(), name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ok := manifestFor("BK-20261013-100000-0001", []byte("ok"))
	write(ok.BackupID+DataExt, []byte("ok"))
	if err := WriteManifest(filepath.Join(s.Dir(), ok.BackupID+ManifestExt), ok); err != nil {
		t.Fatal(err)
	}
	short := manifestFor("BK-20261013-100000-0002", []byte("long data"))
	write(short.BackupID+DataExt, []byte("long"))
	_ = WriteManifest(filepath.Join(s.Dir(), short.BackupID+ManifestExt), short)
	alone := manifestFor("BK-20261013-100000-0003", []byte("gone"))
	_ = WriteManifest(filepath.Join(s.Dir(), alone.BackupID+ManifestExt), alone)
	write("BK-20261013-100000-0004"+DataExt, []byte("orphan"))
	write("notes.txt", []byte("not ours"))

	inv, err := s.Scan()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"BK-20261013-100000-0001": StateVerified, "BK-20261013-100000-0002": StateCorrupt,
		"BK-20261013-100000-0003": StateCorrupt, "BK-20261013-100000-0004": StateCorrupt}
	if len(inv.Backups) != len(want) {
		t.Fatalf("backups %+v", inv.Backups)
	}
	for _, c := range inv.Backups {
		if want[c.BackupID] != c.State {
			t.Errorf("%s = %s, want %s", c.BackupID, c.State, want[c.BackupID])
		}
	}
	if len(inv.Problems) != 3 {
		t.Errorf("problems %q", inv.Problems)
	}
}

func TestScanRemovesStalePartOnce(t *testing.T) {
	s := newStore(t)
	stale := filepath.Join(s.Dir(), "BK-20261013-100000-0001"+PartExt)
	fresh := filepath.Join(s.Dir(), "BK-20261013-100000-0002"+PartExt)
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	_ = os.Chtimes(stale, old, old)
	inv, _ := s.Scan()
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Error("stale .part kept")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("fresh .part removed")
	}
	if len(inv.Problems) != 1 || !strings.Contains(inv.Problems[0], "removed partial transfer") {
		t.Errorf("problems %q", inv.Problems)
	}
	if again, _ := s.Scan(); len(again.Problems) != 0 {
		t.Errorf("reported twice: %q", again.Problems)
	}
}

func TestOpenRemovesLeftovers(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"BK-20261013-100000-0001" + PartExt, "BK-20261013-100000-0001" + ManifestExt + ".tmp"} {
		_ = os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600)
	}
	s, err := Open(dir, 0, discard)
	if err != nil {
		t.Fatal(err)
	}
	if n := names(t, dir); len(n) != 0 {
		t.Errorf("left %v", n)
	}
	if inv, _ := s.Scan(); len(inv.Problems) != 2 {
		t.Errorf("problems %q", inv.Problems)
	}
}

func TestNewID(t *testing.T) {
	id := NewID(time.Date(2026, 10, 13, 10, 15, 0, 0, time.Local))
	if !ValidID(id) || !strings.HasPrefix(id, "BK-20261013-101500-") {
		t.Errorf("NewID = %q", id)
	}
}

func TestFreeBytes(t *testing.T) {
	if n, err := FreeBytes(t.TempDir()); err != nil || n <= 0 {
		t.Errorf("FreeBytes = %d, %v", n, err)
	}
}
```

Run: `go test ./inventory/` → Expected: FAIL (package has no non-test files / undefined `Open`).

- [ ] **Step 2: Implement `inventory/manifest.go`**

```go
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
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
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
```

- [ ] **Step 3: Implement free space**

`inventory/diskfree_unix.go`:

```go
//go:build !windows

package inventory

import "golang.org/x/sys/unix"

// FreeBytes is the space available to this process on dir's filesystem.
func FreeBytes(dir string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
```

`inventory/diskfree_windows.go`:

```go
package inventory

import "golang.org/x/sys/windows"

// FreeBytes is the space available to this process on dir's volume.
func FreeBytes(dir string) (int64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var avail uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, nil, nil); err != nil {
		return 0, err
	}
	return int64(avail), nil
}
```

- [ ] **Step 4: Implement `inventory/store.go`**

```go
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
```

In `logx/logx.go`, add `EventPartRemoved = "part_removed"` to the const block.

- [ ] **Step 5: Run the tests**

Run: `go test -race ./inventory/ && GOOS=windows go vet ./inventory/ && GOOS=linux go vet ./inventory/`
Expected: PASS, no vet output.

- [ ] **Step 6: Commit**

```bash
git add inventory logx
git commit -m "feat(inventory): node store receives backups by hash, scans states and removes stale partial files"
```

---

### Task 4: Dump (`dump`, `dump/dumptest`)

**Files:**
- Create: `dump/dumptest/fake.go`, `dump/tools.go`, `dump/detect.go`, `dump/run.go`, `dump/dump_test.go`

**Interfaces:**
- Consumes:
  - `inventory.Manifest`, `inventory.WriteManifest`
  - `inventory.DataExt`, `inventory.ManifestExt`, `inventory.PartExt`
- Produces:
  - `dump.Tools{Dumper, Client string}` (json `dumper`, `client`), `dump.FindTools(configured string) (Tools, error)`.
  - `dump.Conn{Host string; Port int; User, Password string}`.
  - `dump.Database{Name string; Tables int; NonInnoDB []string}` with `(Database).Consistency() string`.
  - `dump.Detection{Tools Tools; ServerVersion, Flavour string; Databases []Database}`, `dump.Detect(ctx, Tools, Conn) (Detection, error)`.
  - `dump.ContentFlags`.
  - `dump.Options{ID, Database, StagingDir, SourceAgent string; Created time.Time; Tags []string; Tools Tools; Conn Conn; Progress func(int64)}`, `dump.Run(ctx, Options) (inventory.Manifest, error)`.
  - `dump.CleanStaging(dir) ([]string, error)`.
  - `dumptest`:
    - `dumptest.Main()`, `dumptest.Use(t testing.TB, mode string) (bin, log string)`, `dumptest.Dump(db string) string`.
    - Modes: `OK`, `Denied`, `DumpFails`, `DumpWarnsError`, `Hang`.

- [ ] **Step 1: Write the fake (test support, no test of its own)**

`dump/dumptest/fake.go`:

```go
// Package dumptest runs a test binary as a fake mysqldump and mysql client,
// so tests need no database server: call Main first in TestMain, then Use.
package dumptest

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	envMode = "SEVJALA_FAKE_DB"
	envLog  = "SEVJALA_FAKE_DB_LOG"

	OK             = "ok"
	Denied         = "denied"
	DumpFails      = "dumpfail"
	DumpWarnsError = "dumperror"
	Hang           = "hang"
)

// Main turns the process into the fake when a test asked for one.
func Main() {
	if mode := os.Getenv(envMode); mode != "" {
		os.Exit(fake(mode, os.Args[1:]))
	}
}

// Use makes the test binary the fake for the rest of the test. Every
// invocation appends "<args> MYSQL_PWD=<value>" to log.
func Use(t testing.TB, mode string) (bin, log string) {
	t.Helper()
	log = filepath.Join(t.TempDir(), "calls.log")
	t.Setenv(envMode, mode)
	t.Setenv(envLog, log)
	return os.Args[0], log
}

// Dump is what the fake dumper writes for db.
func Dump(db string) string {
	return fmt.Sprintf("-- MariaDB dump 10.19\n-- Database: %s\nCREATE TABLE t (id int);\n", db) +
		strings.Repeat("INSERT INTO t VALUES (1);\n", 2000)
}

func fake(mode string, args []string) int {
	if p := os.Getenv(envLog); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "%s MYSQL_PWD=%s\n", strings.Join(args, " "), os.Getenv("MYSQL_PWD"))
			f.Close()
		}
	}
	if mode == Denied {
		fmt.Fprintf(os.Stderr, "ERROR 1045 (28000): Access denied for user 'root'@'localhost' (password %s)\n", os.Getenv("MYSQL_PWD"))
		return 1
	}
	if i := slices.Index(args, "-e"); i >= 0 && i+1 < len(args) {
		return client(args[i+1])
	}
	return dumper(mode, args[len(args)-1])
}

func client(sql string) int {
	switch {
	case strings.Contains(sql, "VERSION()"):
		fmt.Println("10.4.32-MariaDB")
	case strings.HasPrefix(sql, "SHOW DATABASES"):
		fmt.Print("information_schema\nlegacy\nmysql\nperformance_schema\nshop\n")
	case strings.Contains(sql, "information_schema.TABLES"):
		fmt.Print("legacy\tposts\tMyISAM\nlegacy\tusers\tInnoDB\nshop\titems\tInnoDB\nshop\torders\tInnoDB\n")
	default:
		fmt.Fprintf(os.Stderr, "ERROR 1064: unexpected query %q\n", sql)
		return 1
	}
	return 0
}

func dumper(mode, db string) int {
	switch mode {
	case DumpFails:
		fmt.Fprintf(os.Stderr, "mysqldump: Got error: 1049: \"Unknown database '%s'\" when selecting the database\n", db)
		return 2
	case DumpWarnsError:
		fmt.Printf("-- partial dump of %s\n", db)
		fmt.Fprintln(os.Stderr, "mysqldump: Error 2013: Lost connection to server during query")
		return 0
	case Hang:
		fmt.Printf("-- dump of %s\n", db)
		time.Sleep(time.Hour)
		return 0
	}
	fmt.Print(Dump(db))
	return 0
}
```

- [ ] **Step 2: Write the failing dump tests**

`dump/dump_test.go`:

```go
package dump

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/inventory"
)

func TestMain(m *testing.M) {
	dumptest.Main()
	os.Exit(m.Run())
}

const pw = `p a$s"wörd`

var conn = Conn{Host: "127.0.0.1", Port: 3306, User: "root", Password: pw}

func fakeTools(t *testing.T, mode string) (Tools, string) {
	bin, log := dumptest.Use(t, mode)
	return Tools{Dumper: bin, Client: bin}, log
}

func calls(t *testing.T, log string) []string {
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(raw)), "\n")
}

func TestFindTools(t *testing.T) {
	join := filepath.Join
	tests := []struct {
		name, goos, configured string
		files                  []string
		path                   map[string]string
		want                   Tools
		wantErr                string
	}{
		{name: "configured", goos: "linux", configured: "/opt/m/bin/mysqldump",
			files: []string{"/opt/m/bin/mysqldump", join("/opt/m/bin", "mysql")},
			want:  Tools{"/opt/m/bin/mysqldump", join("/opt/m/bin", "mysql")}},
		{name: "configured missing", goos: "linux", configured: "/nope/mysqldump", wantErr: "--dumper"},
		{name: "xampp windows", goos: "windows",
			files: []string{join(`C:\xampp\mysql\bin`, "mysqldump.exe"), join(`C:\xampp\mysql\bin`, "mysql.exe")},
			want:  Tools{join(`C:\xampp\mysql\bin`, "mysqldump.exe"), join(`C:\xampp\mysql\bin`, "mysql.exe")}},
		{name: "path", goos: "linux", path: map[string]string{"mysqldump": "/usr/bin/mysqldump"},
			files: []string{join("/usr/bin", "mysql")}, want: Tools{"/usr/bin/mysqldump", join("/usr/bin", "mysql")}},
		{name: "mariadb names", goos: "linux", path: map[string]string{"mariadb-dump": "/usr/bin/mariadb-dump"},
			files: []string{join("/usr/bin", "mariadb")}, want: Tools{"/usr/bin/mariadb-dump", join("/usr/bin", "mariadb")}},
		{name: "none", goos: "darwin", wantErr: "mysqldump not found"},
		{name: "no client", goos: "linux", path: map[string]string{"mysqldump": "/usr/bin/mysqldump"}, wantErr: "client not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lookPath := func(n string) (string, error) {
				if p, ok := tt.path[n]; ok {
					return p, nil
				}
				return "", errors.New("not found")
			}
			exists := func(p string) bool { return slices.Contains(tt.files, p) }
			got, err := findTools(tt.configured, tt.goos, lookPath, exists)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
}

func TestDetect(t *testing.T) {
	tools, _ := fakeTools(t, dumptest.OK)
	d, err := Detect(context.Background(), tools, conn)
	if err != nil {
		t.Fatal(err)
	}
	if d.ServerVersion != "10.4.32-MariaDB" || d.Flavour != "MariaDB" || d.Tools != tools {
		t.Errorf("detection %+v", d)
	}
	want := []Database{{Name: "legacy", Tables: 2, NonInnoDB: []string{"posts (MyISAM)"}}, {Name: "shop", Tables: 2}}
	if !reflect.DeepEqual(d.Databases, want) {
		t.Errorf("databases %+v, want %+v", d.Databases, want)
	}
	if d.Databases[0].Consistency() != "--lock-tables" || d.Databases[1].Consistency() != "--single-transaction" {
		t.Error("consistency flags")
	}
}

func TestPasswordOnlyInEnvAndScrubbed(t *testing.T) {
	tools, log := fakeTools(t, dumptest.OK)
	if _, err := Detect(context.Background(), tools, conn); err != nil {
		t.Fatal(err)
	}
	for _, line := range calls(t, log) {
		args, env, _ := strings.Cut(line, " MYSQL_PWD=")
		if strings.Contains(args, "wörd") || env != pw {
			t.Errorf("call %q", line)
		}
	}
	tools, _ = fakeTools(t, dumptest.Denied)
	_, err := Detect(context.Background(), tools, conn)
	if err == nil || !strings.Contains(err.Error(), "Access denied") || strings.Contains(err.Error(), "wörd") {
		t.Errorf("err = %v", err)
	}
}

func TestInheritedPasswordDropped(t *testing.T) {
	t.Setenv("MYSQL_PWD", "leak")
	tools, log := fakeTools(t, dumptest.OK)
	if _, err := Detect(context.Background(), tools, Conn{Host: "127.0.0.1", Port: 3306, User: "root"}); err != nil {
		t.Fatal(err)
	}
	for _, line := range calls(t, log) {
		if !strings.HasSuffix(line, " MYSQL_PWD=") {
			t.Errorf("call %q", line)
		}
	}
}

func opts(t *testing.T, mode, db string) (Options, string) {
	tools, log := fakeTools(t, mode)
	return Options{ID: "BK-20261013-101500-A8F2", Database: db, StagingDir: t.TempDir(), SourceAgent: "c960acf5",
		Created: time.Date(2026, 10, 13, 10, 15, 0, 0, time.UTC), Tags: []string{"manual"}, Tools: tools, Conn: conn}, log
}

func TestRun(t *testing.T) {
	o, log := opts(t, dumptest.OK, "shop")
	var progress int64
	o.Progress = func(n int64) { progress = n }
	m, err := Run(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(o.StagingDir, o.ID+inventory.DataExt))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if m.SHA256 != hex.EncodeToString(sum[:]) || m.Size != int64(len(raw)) || progress != m.Size {
		t.Errorf("manifest %+v, file %d bytes, progress %d", m, len(raw), progress)
	}
	zr, _ := gzip.NewReader(strings.NewReader(string(raw)))
	if plain, _ := io.ReadAll(zr); string(plain) != dumptest.Dump("shop") {
		t.Error("gunzipped content differs from the dump")
	}
	if !slices.Contains(m.DumpFlags, "--single-transaction") || !slices.Contains(m.DumpFlags, "--default-character-set=utf8mb4") ||
		m.ServerVersion != "10.4.32-MariaDB" || m.BackupID != o.ID || m.Database != "shop" || m.V != 1 {
		t.Errorf("manifest %+v", m)
	}
	onDisk, err := inventory.ReadManifest(filepath.Join(o.StagingDir, o.ID+inventory.ManifestExt))
	if err != nil || onDisk.SHA256 != m.SHA256 || !onDisk.CreatedAt.Equal(m.CreatedAt) {
		t.Errorf("manifest on disk %+v, %v", onDisk, err)
	}
	if _, err := os.Stat(filepath.Join(o.StagingDir, o.ID+inventory.PartExt)); !errors.Is(err, os.ErrNotExist) {
		t.Error(".part left")
	}
	all := calls(t, log)
	if dumpCall := all[len(all)-1]; !strings.Contains(dumpCall, "--single-transaction shop MYSQL_PWD=") {
		t.Errorf("dump call %q", dumpCall)
	}
}

func TestRunLockTables(t *testing.T) {
	o, _ := opts(t, dumptest.OK, "legacy")
	m, err := Run(context.Background(), o)
	if err != nil || !slices.Contains(m.DumpFlags, "--lock-tables") {
		t.Errorf("%+v, %v", m, err)
	}
}

func TestRunFailuresLeaveNothing(t *testing.T) {
	tests := []struct{ name, mode, db, want string }{
		{"unknown database", dumptest.OK, "nope", "does not exist"},
		{"dumper exits non-zero", dumptest.DumpFails, "shop", "Unknown database"},
		{"error on stderr", dumptest.DumpWarnsError, "shop", "Lost connection"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o, _ := opts(t, tt.mode, tt.db)
			if _, err := Run(context.Background(), o); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
			if es, _ := os.ReadDir(o.StagingDir); len(es) != 0 {
				t.Errorf("staging not empty: %v", es)
			}
		})
	}
}

func TestRunCancelKillsDumper(t *testing.T) {
	o, _ := opts(t, dumptest.Hang, "shop")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := Run(ctx, o); err == nil {
		t.Fatal("cancelled dump succeeded")
	}
	if time.Since(start) > 10*time.Second {
		t.Error("dumper not killed")
	}
	if es, _ := os.ReadDir(o.StagingDir); len(es) != 0 {
		t.Errorf("staging not empty: %v", es)
	}
}

func TestCleanStaging(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "BK-20261013-101500-A8F2.part"), []byte("x"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "BK-20261013-101500-A8F1.sql.gz"), []byte("x"), 0o600)
	removed, err := CleanStaging(dir)
	if err != nil || len(removed) != 1 || removed[0] != "BK-20261013-101500-A8F2.part" {
		t.Errorf("removed %v, %v", removed, err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Errorf("left %v", es)
	}
}
```

Run: `go test ./dump/` → Expected: FAIL (undefined `findTools`, `Detect`, …).

- [ ] **Step 3: Implement `dump/tools.go`**

```go
// Package dump finds the MySQL/MariaDB tools, detects databases and streams
// one mysqldump into a gzip'd, hashed staging file (blueprint: Backup engine).
package dump

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type Tools struct {
	Dumper string `json:"dumper"`
	Client string `json:"client"`
}

func xamppBin(goos string) string {
	switch goos {
	case "windows":
		return `C:\xampp\mysql\bin`
	case "darwin":
		return "/Applications/XAMPP/xamppfiles/bin"
	}
	return "/opt/lampp/bin"
}

// FindTools picks the dumper (the configured path, else XAMPP's, else
// mysqldump or mariadb-dump on PATH) and the mysql client next to it, which
// detection and restore use instead of a Go MySQL driver.
func FindTools(configured string) (Tools, error) {
	return findTools(configured, runtime.GOOS, exec.LookPath, fileExists)
}

func findTools(configured, goos string, lookPath func(string) (string, error), exists func(string) bool) (Tools, error) {
	exe := func(name string) string {
		if goos == "windows" {
			return name + ".exe"
		}
		return name
	}
	xampp := xamppBin(goos)
	var dumper string
	switch {
	case configured != "":
		if !exists(configured) {
			return Tools{}, fmt.Errorf("dumper %s does not exist; fix it with 'sevjala db set --dumper <path>'", configured)
		}
		dumper = configured
	case exists(filepath.Join(xampp, exe("mysqldump"))):
		dumper = filepath.Join(xampp, exe("mysqldump"))
	default:
		for _, name := range []string{"mysqldump", "mariadb-dump"} {
			if p, err := lookPath(name); err == nil {
				dumper = p
				break
			}
		}
	}
	if dumper == "" {
		return Tools{}, fmt.Errorf("mysqldump not found in %s or on PATH; point at it with 'sevjala db set --dumper <path>'", xampp)
	}
	dir := filepath.Dir(dumper)
	for _, name := range []string{"mysql", "mariadb"} {
		if p := filepath.Join(dir, exe(name)); exists(p) {
			return Tools{Dumper: dumper, Client: p}, nil
		}
	}
	for _, name := range []string{"mysql", "mariadb"} {
		if p, err := lookPath(name); err == nil {
			return Tools{Dumper: dumper, Client: p}, nil
		}
	}
	return Tools{}, fmt.Errorf("mysql client not found next to %s or on PATH", dumper)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
```

- [ ] **Step 4: Implement `dump/detect.go`**

```go
package dump

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

type Conn struct {
	Host     string
	Port     int
	User     string
	Password string
}

func (c Conn) args() []string {
	return []string{"--host=" + c.Host, "--port=" + strconv.Itoa(c.Port), "--user=" + c.User}
}

// env hands the password over the only way that keeps it off the command
// line and off disk; an inherited MYSQL_PWD never leaks through.
func (c Conn) env() []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "MYSQL_PWD=") })
	if c.Password != "" {
		env = append(env, "MYSQL_PWD="+c.Password)
	}
	return env
}

func (c Conn) scrub(s string) string {
	if c.Password == "" {
		return s
	}
	return strings.ReplaceAll(s, c.Password, "***")
}

type Database struct {
	Name      string
	Tables    int
	NonInnoDB []string // "table (ENGINE)"
}

// Consistency is --single-transaction unless a table cannot take part in a
// transaction, which needs --lock-tables instead.
func (d Database) Consistency() string {
	if len(d.NonInnoDB) > 0 {
		return "--lock-tables"
	}
	return "--single-transaction"
}

type Detection struct {
	Tools         Tools
	ServerVersion string
	Flavour       string
	Databases     []Database
}

var systemDBs = []string{"information_schema", "performance_schema", "mysql", "sys"}

func Detect(ctx context.Context, tools Tools, conn Conn) (Detection, error) {
	d := Detection{Tools: tools, Flavour: "MySQL"}
	rows, err := query(ctx, tools, conn, "SELECT VERSION()")
	if err != nil {
		return Detection{}, err
	}
	if len(rows) == 0 {
		return Detection{}, errors.New("SELECT VERSION() returned nothing")
	}
	d.ServerVersion = rows[0][0]
	if strings.Contains(strings.ToLower(d.ServerVersion), "mariadb") {
		d.Flavour = "MariaDB"
	}
	if rows, err = query(ctx, tools, conn, "SHOW DATABASES"); err != nil {
		return Detection{}, err
	}
	for _, r := range rows {
		if !slices.Contains(systemDBs, r[0]) {
			d.Databases = append(d.Databases, Database{Name: r[0]})
		}
	}
	rows, err = query(ctx, tools, conn, "SELECT TABLE_SCHEMA, TABLE_NAME, IFNULL(ENGINE, '') FROM information_schema.TABLES "+
		"WHERE TABLE_TYPE = 'BASE TABLE' ORDER BY TABLE_SCHEMA, TABLE_NAME")
	if err != nil {
		return Detection{}, err
	}
	for _, r := range rows {
		i := slices.IndexFunc(d.Databases, func(db Database) bool { return db.Name == r[0] })
		if i < 0 || len(r) < 3 {
			continue
		}
		d.Databases[i].Tables++
		if !strings.EqualFold(r[2], "InnoDB") {
			d.Databases[i].NonInnoDB = append(d.Databases[i].NonInnoDB, fmt.Sprintf("%s (%s)", r[1], r[2]))
		}
	}
	return d, nil
}

func query(ctx context.Context, tools Tools, conn Conn, sql string) ([][]string, error) {
	args := append(conn.args(), "--batch", "--skip-column-names", "-e", sql)
	cmd := exec.CommandContext(ctx, tools.Client, args...)
	cmd.Env = conn.env()
	stderr := &capped{max: 64 << 10}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s: %v: %s", filepath.Base(tools.Client), err, strings.TrimSpace(conn.scrub(stderr.String())))
	}
	var rows [][]string
	for line := range strings.Lines(string(out)) {
		if line = strings.TrimRight(line, "\r\n"); line != "" {
			rows = append(rows, strings.Split(line, "\t"))
		}
	}
	return rows, nil
}

// capped keeps the first max bytes written to it.
type capped struct {
	buf bytes.Buffer
	max int
}

func (c *capped) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (c *capped) String() string { return c.buf.String() }
```

- [ ] **Step 5: Implement `dump/run.go`**

```go
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
		return inventory.Manifest{}, fmt.Errorf("%s %s: %v: %s", filepath.Base(o.Tools.Dumper), o.Database, err, msg)
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
```

- [ ] **Step 6: Run the tests**

Run: `go test -race ./dump/... && GOOS=windows go vet ./dump/...`
Expected: PASS, no vet output.

- [ ] **Step 7: Commit**

```bash
git add dump
git commit -m "feat(dump): find mysqldump and the client, detect databases and stream a gzip'd hashed dump into staging"
```

---

### Task 5: Controller store schema v2

**Files:**
- Modify: `store/store.go` (migration 2)
- Create: `store/backups.go`, `store/backups_test.go`

**Interfaces:**
- Produces:
  - Constants: `store.BackupCreated`, `BackupDistributed`, `BackupPartial`; `store.CopyPending`, `CopyTransferring`, `CopyVerified`, `CopyFailed`, `CopyOffline`.
  - `store.Backup{BackupID, Database string; CreatedAt time.Time; Size int64; SHA256 string; Tags []string; StagingPath, State string; Copies []Copy}`.
  - `store.Copy{BackupID, AgentID, State string; Attempts int; LastError string; VerifiedAt time.Time}`.
  - `(*Store).InsertBackup(ctx, Backup, agentIDs []string) error`.
  - `(*Store).Backups(ctx) ([]Backup, error)` (newest first).
  - `(*Store).Backup(ctx, id) (Backup, error)` (`ErrNotFound`).
  - `(*Store).SetCopy(ctx, Copy) error`.
  - `(*Store).Unsettled(ctx) ([]Copy, error)`.

- [ ] **Step 1: Write the failing tests**

`store/backups_test.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func backup(id string) Backup {
	return Backup{BackupID: id, Database: "shop", CreatedAt: time.Date(2026, 10, 13, 10, 15, 0, 0, time.UTC),
		Size: 42, SHA256: "ab", Tags: []string{"manual"}, StagingPath: id + ".sql.gz"}
}

func TestMigrateV1ToV2KeepsAgents(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 1; INSERT INTO agents (agent_id, hostname, last_ip, port, fp, role, version, paired_at)
		VALUES ('n1', 'PC', '10.0.0.2', 17891, 'sha256:x', 'node', '0.1.0', '2026-10-12T09:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var v int
	_ = s.db.QueryRow("PRAGMA user_version").Scan(&v)
	if v != 2 {
		t.Errorf("user_version %d", v)
	}
	if _, err := s.Agent(context.Background(), "n1"); err != nil {
		t.Errorf("agent lost: %v", err)
	}
}

func TestInsertAndListBackups(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	if err := s.InsertBackup(ctx, backup("BK-20261013-100000-0001"), []string{"n1", "n2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertBackup(ctx, backup("BK-20261013-110000-0002"), nil); err != nil {
		t.Fatal(err)
	}
	bs, err := s.Backups(ctx)
	if err != nil || len(bs) != 2 || bs[0].BackupID != "BK-20261013-110000-0002" {
		t.Fatalf("backups %+v, %v", bs, err)
	}
	b := bs[1]
	want := backup("BK-20261013-100000-0001")
	want.State = BackupCreated
	want.Copies = []Copy{{BackupID: want.BackupID, AgentID: "n1", State: CopyPending}, {BackupID: want.BackupID, AgentID: "n2", State: CopyPending}}
	if !reflect.DeepEqual(b, want) {
		t.Errorf("got  %+v\nwant %+v", b, want)
	}
	if one, err := s.Backup(ctx, want.BackupID); err != nil || !reflect.DeepEqual(one, want) {
		t.Errorf("Backup = %+v, %v", one, err)
	}
	if _, err := s.Backup(ctx, "BK-20261013-120000-0003"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
}

func TestSetCopyRecomputesBackupState(t *testing.T) {
	tests := []struct {
		name   string
		states []string
		want   string
	}{
		{"all verified", []string{CopyVerified, CopyVerified}, BackupDistributed},
		{"one failed", []string{CopyVerified, CopyFailed}, BackupPartial},
		{"one offline", []string{CopyOffline, CopyVerified}, BackupPartial},
		{"one still moving", []string{CopyVerified, CopyTransferring}, BackupCreated},
		{"one waiting to retry", []string{CopyFailed, CopyPending}, BackupCreated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			s := openTest(t)
			id := "BK-20261013-100000-0001"
			_ = s.InsertBackup(ctx, backup(id), []string{"n1", "n2"})
			for i, st := range tt.states {
				if err := s.SetCopy(ctx, Copy{BackupID: id, AgentID: []string{"n1", "n2"}[i], State: st, Attempts: 1}); err != nil {
					t.Fatal(err)
				}
			}
			if b, _ := s.Backup(ctx, id); b.State != tt.want {
				t.Errorf("state %s, want %s", b.State, tt.want)
			}
		})
	}
}

func TestSetCopyKeepsLastErrorUntilVerified(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	id := "BK-20261013-100000-0001"
	_ = s.InsertBackup(ctx, backup(id), []string{"n1"})
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n1", State: CopyPending, Attempts: 1, LastError: "connection refused"})
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n1", State: CopyTransferring, Attempts: 2})
	if b, _ := s.Backup(ctx, id); b.Copies[0].LastError != "connection refused" || b.Copies[0].Attempts != 2 {
		t.Errorf("copy %+v", b.Copies[0])
	}
	at := time.Date(2026, 10, 13, 10, 20, 0, 0, time.UTC)
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n1", State: CopyVerified, Attempts: 2, VerifiedAt: at})
	if b, _ := s.Backup(ctx, id); b.Copies[0].LastError != "" || !b.Copies[0].VerifiedAt.Equal(at) {
		t.Errorf("copy %+v", b.Copies[0])
	}
	if err := s.SetCopy(ctx, Copy{BackupID: id, AgentID: "nope", State: CopyFailed}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown copy: %v", err)
	}
}

func TestUnsettled(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	id := "BK-20261013-100000-0001"
	_ = s.InsertBackup(ctx, backup(id), []string{"n1", "n2", "n3"})
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n1", State: CopyVerified})
	_ = s.SetCopy(ctx, Copy{BackupID: id, AgentID: "n2", State: CopyTransferring})
	cs, err := s.Unsettled(ctx)
	if err != nil || len(cs) != 2 || cs[0].AgentID != "n2" || cs[1].AgentID != "n3" {
		t.Errorf("unsettled %+v, %v", cs, err)
	}
}
```

Run: `go test ./store/` → Expected: FAIL (undefined `Backup`).

- [ ] **Step 2: Add migration 2**

In `store/store.go`, append to `migrations`:

```go
	`CREATE TABLE backups (
		backup_id    TEXT PRIMARY KEY,
		"database"   TEXT NOT NULL,
		created_at   TEXT NOT NULL,
		size         INTEGER NOT NULL,
		sha256       TEXT NOT NULL,
		tags         TEXT NOT NULL,
		staging_path TEXT NOT NULL,
		state        TEXT NOT NULL
	);
	CREATE TABLE backup_copies (
		backup_id   TEXT NOT NULL REFERENCES backups(backup_id),
		agent_id    TEXT NOT NULL,
		state       TEXT NOT NULL,
		attempts    INTEGER NOT NULL DEFAULT 0,
		last_error  TEXT NOT NULL DEFAULT '',
		verified_at TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (backup_id, agent_id)
	)`,
```

If the driver runs only the first statement of a multi-statement `Exec` (the migrate test then fails with `no such table: backup_copies`), split it into two migrations and make `TestMigrateV1ToV2KeepsAgents` expect `len(migrations)`. Record the outcome as a ledger ruling.

- [ ] **Step 3: Implement `store/backups.go`**

```go
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	BackupCreated     = "created"
	BackupDistributed = "distributed"
	BackupPartial     = "partial"

	CopyPending      = "pending"
	CopyTransferring = "transferring"
	CopyVerified     = "verified"
	CopyFailed       = "failed"
	CopyOffline      = "offline"
)

type Backup struct {
	BackupID    string
	Database    string
	CreatedAt   time.Time
	Size        int64
	SHA256      string
	Tags        []string
	StagingPath string
	State       string
	Copies      []Copy
}

type Copy struct {
	BackupID   string
	AgentID    string
	State      string
	Attempts   int
	LastError  string
	VerifiedAt time.Time
}

const backupCols = `backup_id, "database", created_at, size, sha256, tags, staging_path, state`

// InsertBackup records a staged backup with one pending copy per agent.
func (s *Store) InsertBackup(ctx context.Context, b Backup, agentIDs []string) error {
	if b.Tags == nil {
		b.Tags = []string{}
	}
	tags, err := json.Marshal(b.Tags)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO backups (`+backupCols+`) VALUES (?,?,?,?,?,?,?,?)`,
		b.BackupID, b.Database, fmtTime(b.CreatedAt), b.Size, b.SHA256, string(tags), b.StagingPath, BackupCreated); err != nil {
		return err
	}
	for _, id := range agentIDs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO backup_copies (backup_id, agent_id, state) VALUES (?,?,?)`,
			b.BackupID, id, CopyPending); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Backups(ctx context.Context) ([]Backup, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+backupCols+` FROM backups ORDER BY backup_id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Backup
	for rows.Next() {
		b, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	copies, err := s.copies(ctx, `SELECT * FROM backup_copies`)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Copies = copies[out[i].BackupID]
	}
	return out, nil
}

func (s *Store) Backup(ctx context.Context, id string) (Backup, error) {
	b, err := scanBackup(s.db.QueryRowContext(ctx, `SELECT `+backupCols+` FROM backups WHERE backup_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Backup{}, fmt.Errorf("backup %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Backup{}, err
	}
	copies, err := s.copies(ctx, `SELECT * FROM backup_copies WHERE backup_id = ?`, id)
	b.Copies = copies[id]
	return b, err
}

// SetCopy records a copy's new state and recomputes its backup's state. The
// last error survives retries until the copy is verified.
func (s *Store) SetCopy(ctx context.Context, c Copy) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, `UPDATE backup_copies SET state = ?1, attempts = ?2,
		last_error = CASE WHEN ?1 = 'verified' THEN '' WHEN ?3 <> '' THEN ?3 ELSE last_error END,
		verified_at = ?4 WHERE backup_id = ?5 AND agent_id = ?6`,
		c.State, c.Attempts, c.LastError, fmtTime(c.VerifiedAt), c.BackupID, c.AgentID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("copy %s on %s: %w", c.BackupID, c.AgentID, ErrNotFound)
	}
	rows, err := tx.QueryContext(ctx, `SELECT state FROM backup_copies WHERE backup_id = ?`, c.BackupID)
	if err != nil {
		return err
	}
	var states []string
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			rows.Close()
			return err
		}
		states = append(states, st)
	}
	rows.Close()
	if _, err := tx.ExecContext(ctx, `UPDATE backups SET state = ? WHERE backup_id = ?`, backupState(states), c.BackupID); err != nil {
		return err
	}
	return tx.Commit()
}

func backupState(copies []string) string {
	verified := 0
	for _, st := range copies {
		switch st {
		case CopyPending, CopyTransferring:
			return BackupCreated
		case CopyVerified:
			verified++
		}
	}
	if len(copies) == 0 {
		return BackupCreated
	}
	if verified == len(copies) {
		return BackupDistributed
	}
	return BackupPartial
}

// Unsettled lists the copies a restart interrupted.
func (s *Store) Unsettled(ctx context.Context) ([]Copy, error) {
	all, err := s.copies(ctx, `SELECT * FROM backup_copies WHERE state IN ('pending', 'transferring')`)
	if err != nil {
		return nil, err
	}
	var out []Copy
	for _, cs := range all {
		out = append(out, cs...)
	}
	slices.SortFunc(out, func(a, b Copy) int { return cmp.Or(strings.Compare(a.BackupID, b.BackupID), strings.Compare(a.AgentID, b.AgentID)) })
	return out, nil
}

// copies runs a "SELECT * FROM backup_copies …" query, grouped by backup.
func (s *Store) copies(ctx context.Context, query string, args ...any) (map[string][]Copy, error) {
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY backup_id, agent_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Copy{}
	for rows.Next() {
		var c Copy
		var at string
		if err := rows.Scan(&c.BackupID, &c.AgentID, &c.State, &c.Attempts, &c.LastError, &at); err != nil {
			return nil, err
		}
		if c.VerifiedAt, err = parseTime(at); err != nil {
			return nil, err
		}
		out[c.BackupID] = append(out[c.BackupID], c)
	}
	return out, rows.Err()
}

func scanBackup(r scanner) (Backup, error) {
	var b Backup
	var created, tags string
	if err := r.Scan(&b.BackupID, &b.Database, &created, &b.Size, &b.SHA256, &tags, &b.StagingPath, &b.State); err != nil {
		return Backup{}, err
	}
	var err error
	if b.CreatedAt, err = parseTime(created); err != nil {
		return Backup{}, err
	}
	return b, json.Unmarshal([]byte(tags), &b.Tags)
}
```

Add `cmp`, `slices` and `strings` to the imports. `SELECT *` relies on the column order of migration 2: `backup_id, agent_id, state, attempts, last_error, verified_at`.

- [ ] **Step 4: Run the tests**

Run: `go test -race ./store/`
Expected: PASS. If `?1`-style numbered parameters are rejected by the driver, rewrite `SetCopy`'s UPDATE with plain `?`, repeating the arguments in order, and keep the test unchanged.

- [ ] **Step 5: Commit**

```bash
git add store
git commit -m "feat(store): schema v2 with backups and per-node copies; backup state follows its copies"
```

---

### Task 6: Transfer pool (`transfer`)

**Files:**
- Create: `transfer/transfer.go`, `transfer/transfer_test.go`

**Interfaces:**
- Consumes:
  - `store.CopyTransferring`, `store.CopyPending`
  - `store.CopyVerified`, `store.CopyFailed`, `store.CopyOffline`
- Produces:
  - `transfer.Job{BackupID, AgentID string}`
  - `transfer.DefaultDelays`
  - `transfer.Permanent(error) error`
  - `transfer.Unreachable(error) error`
  - `transfer.NewPool(push func(context.Context, Job) error, settle func(Job, string, int, error), delays []time.Duration) *Pool`
  - `(*Pool).Enqueue(Job)`
  - `(*Pool).Close()`

- [ ] **Step 1: Write the failing tests**

`transfer/transfer_test.go`:

```go
package transfer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jufianto/sevjala/store"
)

var fast = []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond}

type recorder struct {
	mu     sync.Mutex
	events []string
	done   chan Job
}

func newRecorder() *recorder { return &recorder{done: make(chan Job, 16)} }

func (r *recorder) settle(j Job, state string, attempts int, _ error) {
	r.mu.Lock()
	r.events = append(r.events, fmt.Sprintf("%s %s %d", j.AgentID, state, attempts))
	r.mu.Unlock()
	if state == store.CopyVerified || state == store.CopyFailed || state == store.CopyOffline {
		r.done <- j
	}
}

func (r *recorder) wait(t *testing.T) {
	t.Helper()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("job did not settle")
	}
}

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.events)
}

func run(t *testing.T, push func(context.Context, Job) error) []string {
	r := newRecorder()
	p := NewPool(push, r.settle, fast)
	defer p.Close()
	p.Enqueue(Job{BackupID: "b1", AgentID: "n1"})
	r.wait(t)
	return r.got()
}

func failing(errs ...error) func(context.Context, Job) error {
	var mu sync.Mutex
	i := 0
	return func(context.Context, Job) error {
		mu.Lock()
		defer mu.Unlock()
		if i < len(errs) {
			i++
			return errs[i-1]
		}
		return nil
	}
}

func TestOutcomes(t *testing.T) {
	boom := errors.New("500 internal")
	refused := Unreachable(errors.New("connection refused"))
	tests := []struct {
		name string
		push func(context.Context, Job) error
		want []string
	}{
		{"first try", failing(), []string{"n1 transferring 1", "n1 verified 1"}},
		{"retry then success", failing(boom, boom),
			[]string{"n1 transferring 1", "n1 pending 1", "n1 transferring 2", "n1 pending 2", "n1 transferring 3", "n1 verified 3"}},
		{"retries exhausted", failing(boom, boom, boom, boom), []string{"n1 transferring 1", "n1 pending 1", "n1 transferring 2",
			"n1 pending 2", "n1 transferring 3", "n1 pending 3", "n1 transferring 4", "n1 failed 4"}},
		{"never reachable is offline", failing(refused, refused, refused, refused), []string{"n1 transferring 1", "n1 pending 1",
			"n1 transferring 2", "n1 pending 2", "n1 transferring 3", "n1 pending 3", "n1 transferring 4", "n1 offline 4"}},
		{"reached once is failed", failing(refused, boom, refused, refused), []string{"n1 transferring 1", "n1 pending 1",
			"n1 transferring 2", "n1 pending 2", "n1 transferring 3", "n1 pending 3", "n1 transferring 4", "n1 failed 4"}},
		{"permanent is not retried", failing(Permanent(boom)), []string{"n1 transferring 1", "n1 failed 1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := run(t, tt.push); !slices.Equal(got, tt.want) {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
		})
	}
}

func TestCloseCancelsInFlight(t *testing.T) {
	r := newRecorder()
	started := make(chan struct{})
	p := NewPool(func(ctx context.Context, _ Job) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}, r.settle, fast)
	p.Enqueue(Job{BackupID: "b1", AgentID: "n1"})
	<-started
	p.Close()
	if got := r.got(); !slices.Equal(got, []string{"n1 transferring 1"}) {
		t.Errorf("events %q", got)
	}
	p.Enqueue(Job{BackupID: "b2", AgentID: "n2"})
}

func TestOneWorkerPerNodeInOrder(t *testing.T) {
	r := newRecorder()
	var mu sync.Mutex
	var order []string
	p := NewPool(func(_ context.Context, j Job) error {
		mu.Lock()
		order = append(order, j.AgentID+":"+j.BackupID)
		mu.Unlock()
		time.Sleep(5 * time.Millisecond)
		return nil
	}, r.settle, fast)
	defer p.Close()
	for _, j := range []Job{{"a", "n1"}, {"b", "n1"}, {"c", "n2"}} {
		p.Enqueue(j)
	}
	for range 3 {
		r.wait(t)
	}
	mu.Lock()
	defer mu.Unlock()
	if slices.Index(order, "n1:a") > slices.Index(order, "n1:b") {
		t.Errorf("order %q", order)
	}
}
```

Run: `go test ./transfer/` → Expected: FAIL (no Go files / undefined `NewPool`).

- [ ] **Step 2: Implement `transfer/transfer.go`**

```go
// Package transfer pushes staged backups to nodes: one worker per node and
// whole-file retries with backoff (blueprint: Backup engine → Transfer).
package transfer

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jufianto/sevjala/store"
)

type Job struct {
	BackupID string
	AgentID  string
}

// DefaultDelays are the waits before retries 1-3; after them a copy is failed
// (or offline) until M3's /status cycle picks it up again.
var DefaultDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute}

const queueSize = 1024

type permanentError struct{ error }

func (e permanentError) Unwrap() error { return e.error }

// Permanent marks an error a retry cannot fix.
func Permanent(err error) error { return permanentError{err} }

type unreachableError struct{ error }

func (e unreachableError) Unwrap() error { return e.error }

// Unreachable marks an error where the node could not be reached at all.
func Unreachable(err error) error { return unreachableError{err} }

type Pool struct {
	push   func(context.Context, Job) error
	settle func(j Job, state string, attempts int, err error)
	delays []time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	queues map[string]chan Job
}

func NewPool(push func(context.Context, Job) error, settle func(Job, string, int, error), delays []time.Duration) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{push: push, settle: settle, delays: delays, ctx: ctx, cancel: cancel, queues: map[string]chan Job{}}
}

func (p *Pool) Enqueue(j Job) {
	p.mu.Lock()
	if p.ctx.Err() != nil {
		p.mu.Unlock()
		return
	}
	q, ok := p.queues[j.AgentID]
	if !ok {
		q = make(chan Job, queueSize)
		p.queues[j.AgentID] = q
		p.wg.Add(1)
		go p.work(q)
	}
	p.mu.Unlock()
	select {
	case q <- j:
	case <-p.ctx.Done():
	}
}

// Close cancels in-flight pushes and waits for the workers; an interrupted
// copy keeps its pending/transferring state and is requeued at the next start.
func (p *Pool) Close() {
	p.mu.Lock()
	p.cancel()
	p.mu.Unlock()
	p.wg.Wait()
}

func (p *Pool) work(q chan Job) {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case j := <-q:
			p.run(j)
		}
	}
}

func (p *Pool) run(j Job) {
	unreachable := true
	for attempt := 1; ; attempt++ {
		p.settle(j, store.CopyTransferring, attempt, nil)
		err := p.push(p.ctx, j)
		if err == nil {
			p.settle(j, store.CopyVerified, attempt, nil)
			return
		}
		if p.ctx.Err() != nil {
			return
		}
		var ue unreachableError
		if !errors.As(err, &ue) {
			unreachable = false
		}
		var pe permanentError
		if errors.As(err, &pe) || attempt > len(p.delays) {
			state := store.CopyFailed
			if unreachable {
				state = store.CopyOffline
			}
			p.settle(j, state, attempt, err)
			return
		}
		p.settle(j, store.CopyPending, attempt, err)
		select {
		case <-p.ctx.Done():
			return
		case <-time.After(p.delays[attempt-1]):
		}
	}
}
```

- [ ] **Step 3: Run the tests**

Run: `go test -race ./transfer/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add transfer
git commit -m "feat(transfer): per-node worker pool with 30s/2m/10m whole-file retries"
```

---

### Task 7: LAN API backups (`PUT /backups/{id}`, full `/status`)

**Files:**
- Modify: `lanapi/types.go`, `lanapi/server.go`, `lanapi/client.go`, `lanapi/lanapi_test.go`, `logx/logx.go`

**Interfaces:**
- Consumes:
  - `inventory.Store` with `Receive`, `Scan`, `Dir` and `FreeBytes`.
  - `inventory.Manifest`.
  - The `inventory.Err*` errors.
- Produces:
  - `lanapi.HeaderSHA256`, `HeaderSize`, `HeaderManifest`.
  - `lanapi.PutResponse{BackupID, SHA256 string; Size int64}`.
  - `lanapi.StoreInfo{Path string; FreeBytes int64}`.
  - `Status.Store *StoreInfo`.
  - `Options.Store *inventory.Store`.
  - `(*Client).PutBackup(ctx, addr, nodeFP string, m inventory.Manifest, body io.Reader) error`.
  - `Client.Stall time.Duration` (default 60 s).
  - `logx.EventBackupReceived = "backup_received"`, `EventBackupRejected = "backup_rejected"`.

- [ ] **Step 1: Write the failing tests**

Append to `lanapi/lanapi_test.go` (add imports `bytes`, `crypto/rand`, `crypto/sha256`, `encoding/hex`, `io`, `os`, `path/filepath`, `github.com/jufianto/sevjala/inventory`):

```go
func startStoreNode(t *testing.T) (agent, *inventory.Store) {
	t.Helper()
	id, dir := newID(t)
	st, err := inventory.Open(filepath.Join(dir, "store"), 0, discard)
	if err != nil {
		t.Fatal(err)
	}
	codes := pairing.New(time.Now)
	s, err := Start(Options{Addr: "127.0.0.1:0", DataDir: dir, Identity: id, Role: "node", Version: "test",
		Hostname: "host-node", Started: time.Now(), Codes: codes, Store: st, Log: discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return agent{dir: dir, id: id, codes: codes, srv: s}, st
}

func backupData(t *testing.T, n int) (inventory.Manifest, []byte) {
	t.Helper()
	data := make([]byte, n)
	_, _ = rand.Read(data)
	sum := sha256.Sum256(data)
	return inventory.Manifest{V: 1, BackupID: "BK-20261013-101500-A8F2", Database: "shop",
		CreatedAt: time.Date(2026, 10, 13, 10, 15, 0, 0, time.UTC), SourceAgent: "c1", Size: int64(n),
		SHA256: hex.EncodeToString(sum[:]), Tags: []string{"manual"}}, data
}

func storeFiles(t *testing.T, st *inventory.Store) []string {
	t.Helper()
	es, _ := os.ReadDir(st.Dir())
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

func TestPutBackupThenStatusListsIt(t *testing.T) {
	n, st := startStoreNode(t)
	c := controller(t)
	pair(t, c, n)
	m, data := backupData(t, 1<<20)
	if err := c.PutBackup(context.Background(), n.srv.Addr(), n.id.FP, m, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	s, err := c.Status(context.Background(), n.srv.Addr(), n.id.FP)
	if err != nil {
		t.Fatal(err)
	}
	if s.Store == nil || s.Store.Path != st.Dir() || s.Store.FreeBytes <= 0 || s.Health != "healthy" {
		t.Errorf("status %+v", s)
	}
	if len(s.Backups) != 1 || s.Backups[0] != (BackupRef{BackupID: m.BackupID, Size: m.Size, SHA256: m.SHA256, State: "verified"}) {
		t.Errorf("backups %+v", s.Backups)
	}
}

func TestPutBackupUnpairedRejected(t *testing.T) {
	n, st := startStoreNode(t)
	m, data := backupData(t, 1024)
	err := controller(t).PutBackup(context.Background(), n.srv.Addr(), n.id.FP, m, bytes.NewReader(data))
	if !errors.Is(err, ErrRejected) {
		t.Errorf("err = %v", err)
	}
	if f := storeFiles(t, st); len(f) != 0 {
		t.Errorf("files %v", f)
	}
}

func TestPutBackupMismatchIs422(t *testing.T) {
	n, st := startStoreNode(t)
	c := controller(t)
	pair(t, c, n)
	m, data := backupData(t, 4096)
	data[0] ^= 0xff
	if err := c.PutBackup(context.Background(), n.srv.Addr(), n.id.FP, m, bytes.NewReader(data)); httpCode(err) != 422 {
		t.Errorf("err = %v", err)
	}
	if f := storeFiles(t, st); len(f) != 0 {
		t.Errorf("files %v", f)
	}
}

func TestPutBackupBadIDIs400(t *testing.T) {
	n, _ := startStoreNode(t)
	c := controller(t)
	pair(t, c, n)
	m, data := backupData(t, 16)
	m.BackupID = "not-an-id"
	if err := c.PutBackup(context.Background(), n.srv.Addr(), n.id.FP, m, bytes.NewReader(data)); httpCode(err) != 400 {
		t.Errorf("err = %v", err)
	}
}

type countingReader struct {
	r io.Reader
	n int
}

func (c *countingReader) Read(p []byte) (int, error) { n, err := c.r.Read(p); c.n += n; return n, err }

// A retry after a lost 200 must not resend a file the node already verified.
func TestPutBackupAgainSendsNoBody(t *testing.T) {
	n, _ := startStoreNode(t)
	c := controller(t)
	pair(t, c, n)
	m, data := backupData(t, 1<<20)
	if err := c.PutBackup(context.Background(), n.srv.Addr(), n.id.FP, m, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	cr := &countingReader{r: bytes.NewReader(data)}
	if err := c.PutBackup(context.Background(), n.srv.Addr(), n.id.FP, m, cr); err != nil {
		t.Fatal(err)
	}
	if cr.n != 0 {
		t.Errorf("resent %d bytes", cr.n)
	}
}

type blockingReader struct {
	first []byte
	ctx   context.Context
}

func (b *blockingReader) Read(p []byte) (int, error) {
	if len(b.first) > 0 {
		n := copy(p, b.first)
		b.first = b.first[n:]
		return n, nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}

func waitNoFiles(t *testing.T, st *inventory.Store) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(storeFiles(t, st)) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("files left: %v", storeFiles(t, st))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestPutBackupKilledMidwayLeavesNoPart(t *testing.T) {
	n, st := startStoreNode(t)
	c := controller(t)
	pair(t, c, n)
	m, data := backupData(t, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- c.PutBackup(ctx, n.srv.Addr(), n.id.FP, m, &blockingReader{first: data[:64<<10], ctx: ctx})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(storeFiles(t, st)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("cancelled put succeeded")
	}
	waitNoFiles(t, st)
}

func TestPutBackupStallIsCancelled(t *testing.T) {
	n, st := startStoreNode(t)
	c := controller(t)
	c.Stall = 200 * time.Millisecond
	pair(t, c, n)
	m, data := backupData(t, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	start := time.Now()
	err := c.PutBackup(context.Background(), n.srv.Addr(), n.id.FP, m, &blockingReader{first: data[:1024], ctx: ctx})
	if err == nil || !strings.Contains(err.Error(), "stalled") || time.Since(start) > 5*time.Second {
		t.Errorf("err = %v after %v", err, time.Since(start))
	}
	waitNoFiles(t, st)
}
```

Run: `go test ./lanapi/` → Expected: FAIL (`unknown field Store in struct literal`).

- [ ] **Step 2: Implement the types**

In `lanapi/types.go`, add:

```go
const (
	HeaderSHA256   = "X-Sevjala-Sha256"
	HeaderSize     = "X-Sevjala-Size"
	HeaderManifest = "X-Sevjala-Manifest"
)

type PutResponse struct {
	BackupID string `json:"backup_id"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type StoreInfo struct {
	Path      string `json:"path"`
	FreeBytes int64  `json:"free_bytes"`
}
```

Replace the `Status` comment and struct:

```go
// Status is the blueprint's GET /status; Store is absent on a controller.
type Status struct {
	AgentID  string      `json:"agent_id"`
	Role     string      `json:"role"`
	Version  string      `json:"version"`
	UptimeS  int64       `json:"uptime_s"`
	Store    *StoreInfo  `json:"store,omitempty"`
	Health   string      `json:"health"`
	Backups  []BackupRef `json:"backups"`
	Problems []string    `json:"problems"`
}
```

- [ ] **Step 3: Implement the server**

In `lanapi/server.go`:
- `Options` gains `Store *inventory.Store // nil on a controller`.
- Add the const `bodyIdle = time.Minute`.
- `routes()` gains `mux.HandleFunc("PUT /backups/{id}", s.pairedOnly(s.putBackup))`.
- Replace `status`:

```go
func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	st := Status{AgentID: s.opt.Identity.AgentID, Role: s.opt.Role, Version: s.opt.Version,
		UptimeS: int64(time.Since(s.opt.Started).Seconds()), Health: "healthy", Backups: []BackupRef{}, Problems: []string{}}
	if s.opt.Store != nil {
		st.Store = &StoreInfo{Path: s.opt.Store.Dir()}
		if inv, err := s.opt.Store.Scan(); err != nil {
			st.Problems = append(st.Problems, "cannot read the store: "+err.Error())
		} else {
			for _, c := range inv.Backups {
				st.Backups = append(st.Backups, BackupRef{BackupID: c.BackupID, Size: c.Size, SHA256: c.SHA256, State: c.State})
			}
			st.Problems = append(st.Problems, inv.Problems...)
		}
		if free, err := s.opt.Store.FreeBytes(); err != nil {
			st.Problems = append(st.Problems, "cannot read free space: "+err.Error())
		} else {
			st.Store.FreeBytes = free
		}
	}
	if len(st.Problems) > 0 {
		st.Health = "warning"
	}
	writeJSON(w, http.StatusOK, st)
}

// putBackup stores a pushed backup. The client sends Expect: 100-continue, so
// a refusal before the first body read costs no body bytes.
func (s *Server) putBackup(w http.ResponseWriter, r *http.Request) {
	if s.opt.Store == nil {
		writeError(w, http.StatusConflict, "this agent is a controller; backups go to nodes")
		return
	}
	id := r.PathValue("id")
	m, sha, size, err := backupHeaders(r, id)
	if err == nil {
		body := &deadlineReader{r: r.Body, rc: http.NewResponseController(w)}
		err = s.opt.Store.Receive(m, sha, size, body)
	}
	switch {
	case err == nil:
		s.opt.Log.Info("backup received", logx.Event(logx.EventBackupReceived), "backup_id", id, "size", size, "remote", r.RemoteAddr)
	case errors.Is(err, inventory.ErrExists):
	default:
		s.opt.Log.Warn("backup rejected", logx.Event(logx.EventBackupRejected), "backup_id", id, "reason", err.Error(), "remote", r.RemoteAddr)
		writeError(w, receiveStatus(err), err.Error())
		return
	}
	writeJSON(w, http.StatusOK, PutResponse{BackupID: id, SHA256: sha, Size: size})
}

func backupHeaders(r *http.Request, id string) (inventory.Manifest, string, int64, error) {
	var m inventory.Manifest
	size, err := strconv.ParseInt(r.Header.Get(HeaderSize), 10, 64)
	if err != nil {
		return m, "", 0, fmt.Errorf("%w: %s: %v", inventory.ErrInvalid, HeaderSize, err)
	}
	raw := r.Header.Get(HeaderManifest)
	if len(raw) > 8<<10 {
		return m, "", 0, fmt.Errorf("%w: %s over 8 KiB", inventory.ErrInvalid, HeaderManifest)
	}
	js, err := base64.StdEncoding.DecodeString(raw)
	if err == nil {
		err = json.Unmarshal(js, &m)
	}
	if err != nil {
		return m, "", 0, fmt.Errorf("%w: %s: %v", inventory.ErrInvalid, HeaderManifest, err)
	}
	if m.BackupID != id {
		return m, "", 0, fmt.Errorf("%w: manifest is for %q, not %q", inventory.ErrInvalid, m.BackupID, id)
	}
	return m, r.Header.Get(HeaderSHA256), size, nil
}

func receiveStatus(err error) int {
	switch {
	case errors.Is(err, inventory.ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, inventory.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, inventory.ErrMismatch):
		return http.StatusUnprocessableEntity
	case errors.Is(err, inventory.ErrNoSpace):
		return http.StatusInsufficientStorage
	}
	return http.StatusInternalServerError
}

// deadlineReader frees the slot when the controller stalls: each read must
// arrive within bodyIdle of the previous one.
type deadlineReader struct {
	r  io.Reader
	rc *http.ResponseController
}

func (d *deadlineReader) Read(p []byte) (int, error) {
	_ = d.rc.SetReadDeadline(time.Now().Add(bodyIdle))
	return d.r.Read(p)
}
```

Imports gain `encoding/base64`, `io`, `strconv` and `github.com/jufianto/sevjala/inventory`. In `logx/logx.go`, add `EventBackupReceived = "backup_received"` and `EventBackupRejected = "backup_rejected"`.

- [ ] **Step 4: Implement the client**

In `lanapi/client.go`:
- `Client` gains `Stall time.Duration`, and `NewClient` sets `Stall: time.Minute`.
- Split `http` and `do`, and add `PutBackup`:

```go
func (c *Client) http(wantFP string, withCert bool, gotFP *string) *http.Client {
	return &http.Client{Timeout: c.Timeout, Transport: c.transport(wantFP, withCert, gotFP)}
}

// transport pins the server to wantFP (any server when empty) and records the
// fingerprint it saw in gotFP.
func (c *Client) transport(wantFP string, withCert bool, gotFP *string) *http.Transport {
	cfg := &tls.Config{ /* … the existing tls.Config, unchanged … */ }
	if withCert {
		cfg.Certificates = []tls.Certificate{c.self.Cert}
	}
	return &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}
}

func (c *Client) do(ctx context.Context, hc *http.Client, method, addr, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+addr+path, rd)
	if err != nil {
		return err
	}
	return send(hc, req, addr, out)
}

func send(hc *http.Client, req *http.Request, addr string, out any) error {
	resp, err := hc.Do(req)
	// … the rest of the existing do() from "if err != nil {" to the final Decode, unchanged
}

var errStalled = errors.New("transfer stalled: no progress for too long")

// PutBackup streams body to the node pinned by nodeFP. There is no overall
// timeout (files are hundreds of MB); a watchdog cancels when no bytes moved
// for c.Stall.
func (c *Client) PutBackup(ctx context.Context, addr, nodeFP string, m inventory.Manifest, body io.Reader) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	pr := &progressReader{r: body}
	pr.last.Store(time.Now().UnixNano())
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(c.Stall / 4)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if !pr.done.Load() && time.Since(time.Unix(0, pr.last.Load())) > c.Stall {
					cancel(errStalled)
					return
				}
			}
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "https://"+addr+"/backups/"+url.PathEscape(m.BackupID), pr)
	if err != nil {
		return err
	}
	req.ContentLength = m.Size
	req.Header.Set("Expect", "100-continue")
	req.Header.Set(HeaderSHA256, m.SHA256)
	req.Header.Set(HeaderSize, strconv.FormatInt(m.Size, 10))
	req.Header.Set(HeaderManifest, base64.StdEncoding.EncodeToString(raw))
	tr := c.transport(nodeFP, true, nil)
	tr.ExpectContinueTimeout = 5 * time.Second
	var out PutResponse
	if err := send(&http.Client{Transport: tr}, req, addr, &out); err != nil {
		if errors.Is(context.Cause(ctx), errStalled) {
			return fmt.Errorf("%s: %w", addr, errStalled)
		}
		return err
	}
	if out.SHA256 != m.SHA256 {
		return fmt.Errorf("%s stored sha256 %s, expected %s", addr, out.SHA256, m.SHA256)
	}
	return nil
}

type progressReader struct {
	r    io.Reader
	last atomic.Int64
	done atomic.Bool
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.last.Store(time.Now().UnixNano())
	if err == io.EOF {
		p.done.Store(true)
	}
	return n, err
}
```

Imports gain `encoding/base64`, `net/url`, `strconv`, `sync/atomic` and `github.com/jufianto/sevjala/inventory`.

- [ ] **Step 5: Run the tests**

Run: `go test -race ./lanapi/`
Expected: PASS.
- If `TestPutBackupAgainSendsNoBody` reads bytes, check that the transport waits for the 100 response (`ExpectContinueTimeout` set, `ContentLength` > 0). Fix the client, never the test.
- If `TestPutBackupUnpairedRejected` gets an HTTP 403 instead of `ErrRejected`, the node has an active code. `startStoreNode` never issues one, so investigate.

- [ ] **Step 6: Commit**

```bash
git add lanapi logx
git commit -m "feat(lanapi): PUT /backups/{id} with 100-continue and stall watchdog; /status lists the node store"
```

---

### Task 8: Local API types for M2

**Files:**
- Modify: `localapi/api.go` (types only; the `Backend` interface changes in Task 10)

**Interfaces:**
- Produces:
  - `localapi.DBDatabase{Name string; Tables int; Consistency string; NonInnoDB []string; Configured bool}` (json `name, tables, consistency, non_innodb, configured`).
  - `localapi.Detection{Dumper, Client, ServerVersion, Flavour string; Databases []DBDatabase}` (json `dumper, client, server_version, flavour, databases`).
  - `localapi.DBSettings{Host *string; Port *int; User, Password, Dumper *string; Databases []string}`. JSON tags: `host,omitempty`, `port,omitempty`, `user,omitempty`, `password,omitempty`, `dumper,omitempty`, `databases`. A nil `Databases` keeps the current list; an empty one clears it.
  - `localapi.BackupJob{BackupID, Database string}`.
  - `localapi.BackupCopy{AgentID, Hostname, State string; Attempts int; LastError string; VerifiedAt time.Time}`; JSON tags `last_error,omitempty`, `verified_at,omitzero`.
  - `localapi.Backup{BackupID, Database string; CreatedAt time.Time; Size int64; SHA256 string; Tags []string; State, Error string; Bytes int64; Copies []BackupCopy}`; JSON tags `created_at,omitzero`, `error,omitempty`, `bytes,omitempty`.
  - `localapi.StoreInfo{Path string; FreeBytes int64; Backups int; Problems []string}`.
  - `Status.Store *StoreInfo` (json `store,omitempty`).
  - Constants `localapi.BackupDumping = "dumping"`, `localapi.BackupFailed = "failed"`.

- [ ] **Step 1: Add the types**

Add to `localapi/api.go`:

```go
const (
	BackupDumping = "dumping"
	BackupFailed  = "failed"
)

type DBDatabase struct {
	Name        string   `json:"name"`
	Tables      int      `json:"tables"`
	Consistency string   `json:"consistency"`
	NonInnoDB   []string `json:"non_innodb"`
	Configured  bool     `json:"configured"`
}

type Detection struct {
	Dumper        string       `json:"dumper"`
	Client        string       `json:"client"`
	ServerVersion string       `json:"server_version"`
	Flavour       string       `json:"flavour"`
	Databases     []DBDatabase `json:"databases"`
}

// DBSettings is a partial update: nil fields keep the agent's value; a nil
// Databases keeps the list, an empty one clears it.
type DBSettings struct {
	Host      *string  `json:"host,omitempty"`
	Port      *int     `json:"port,omitempty"`
	User      *string  `json:"user,omitempty"`
	Password  *string  `json:"password,omitempty"`
	Dumper    *string  `json:"dumper,omitempty"`
	Databases []string `json:"databases"`
}

type BackupJob struct {
	BackupID string `json:"backup_id"`
	Database string `json:"database"`
}

type BackupCopy struct {
	AgentID    string    `json:"agent_id"`
	Hostname   string    `json:"hostname"`
	State      string    `json:"state"`
	Attempts   int       `json:"attempts"`
	LastError  string    `json:"last_error,omitempty"`
	VerifiedAt time.Time `json:"verified_at,omitzero"`
}

// Backup is a stored backup, or one still dumping (State BackupDumping,
// Bytes so far) or whose dump failed (State BackupFailed, Error).
type Backup struct {
	BackupID  string       `json:"backup_id"`
	Database  string       `json:"database"`
	CreatedAt time.Time    `json:"created_at,omitzero"`
	Size      int64        `json:"size"`
	SHA256    string       `json:"sha256"`
	Tags      []string     `json:"tags"`
	State     string       `json:"state"`
	Error     string       `json:"error,omitempty"`
	Bytes     int64        `json:"bytes,omitempty"`
	Copies    []BackupCopy `json:"copies"`
}

type StoreInfo struct {
	Path      string   `json:"path"`
	FreeBytes int64    `json:"free_bytes"`
	Backups   int      `json:"backups"`
	Problems  []string `json:"problems"`
}
```

`Status` gains `Store *StoreInfo \`json:"store,omitempty"\`` as its last field.

- [ ] **Step 2: Compile and run the existing tests**

Run: `go build ./... && go test ./localapi/`
Expected: PASS. `TestStatusRoundTrip` still compares with `!=`; `Status` stays comparable because `Store` is a pointer.

- [ ] **Step 3: Commit**

```bash
git add localapi/api.go
git commit -m "feat(localapi): M2 types for database detection, backups and the node store"
```

---

### Task 9: Controller orchestration (`coord`)

**Files:**
- Create: `coord/backup.go`, `coord/database.go`, `coord/backup_test.go`, `coord/main_test.go`
- Modify: `coord/controller.go` (struct fields), `coord/integration_test.go` (`startAgent` gives nodes an inventory store; `newController` fills the new fields), `logx/logx.go`

**Interfaces:**
- Consumes:
  - From `dump`: `FindTools`, `Detect`, `Run`, `Options`, `Conn`.
  - From `dumptest`: `Main`, `Use`.
  - From `inventory`: `NewID`, `ReadManifest`, `DataExt`, `ManifestExt`.
  - From `store`: `InsertBackup`, `Backups`, `Backup`, `SetCopy`, `Unsettled`, and the state constants.
  - From `transfer`: `Pool`, `Job`, `Permanent`, `Unreachable`.
  - From `lanapi`: `PutBackup`, `HTTPError`, `ErrRejected`.
  - From `config`: `Secret`, `SetSecret`, `DeleteSecret`, `Write`, `Path`, `KeyringPrefix`.
  - From `localapi`: the Task 8 types.
- Produces:
  - `Controller` fields:
    - `DataDir`, `Staging string`
    - `Config config.Config` (read and written under `cfgMu` after start)
    - `Ctx context.Context`
    - `FindTools func(string) (dump.Tools, error)`
    - `Pool *transfer.Pool`
  - Methods:
    - `BackupNow(ctx, db string, tags []string) ([]localapi.BackupJob, error)`
    - `Backups(ctx) ([]localapi.Backup, error)`
    - `Backup(ctx, id) (localapi.Backup, error)`
    - `DetectDatabase(ctx) (localapi.Detection, error)`
    - `SetDatabase(ctx, localapi.DBSettings) (localapi.Detection, error)`
    - `Push(ctx, transfer.Job) error`
    - `Settle(transfer.Job, string, int, error)`
    - `Resume(ctx) error`
  - Events:
    - `logx.EventBackupStarted = "backup_started"`, `EventBackupCreated = "backup_created"`, `EventBackupFailed = "backup_failed"`
    - `EventTransferStarted = "transfer_started"`, `EventTransferVerified = "transfer_verified"`, `EventTransferFailed = "transfer_failed"`
    - `EventDBConfigured = "db_configured"`, `EventKeyringFallback = "keyring_fallback"`

- [ ] **Step 1: Add the test scaffolding**

`coord/main_test.go`:

```go
package coord

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/jufianto/sevjala/dump/dumptest"
)

func TestMain(m *testing.M) {
	dumptest.Main()
	keyring.MockInit()
	os.Exit(m.Run())
}
```

In `coord/integration_test.go`:
- `testAgent` gains `inv *inventory.Store`.
- In `startAgent`, for `role == "node"`, open `inv, err := inventory.Open(filepath.Join(dir, "store"), 0, discard)` (fatal on error) and pass `Store: inv` to `lanapi.Options`.
- Return `inv` in the struct.
- `newController` becomes:

```go
func newController(t *testing.T, targets ...*discovery.Responder) *Controller {
	t.Helper()
	dir := t.TempDir()
	id, _, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, store.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cfg := config.Default(config.RoleController)
	cfg.Controller.Database.Databases = []string{"shop"}
	c := &Controller{Self: id, Store: st, LAN: lanapi.NewClient(id), Wait: 300 * time.Millisecond, Log: discard,
		Targets: func() ([]*net.UDPAddr, error) {
			var out []*net.UDPAddr
			for _, r := range targets {
				out = append(out, r.Addr())
			}
			return out, nil
		},
		DataDir: dir, Staging: staging, Config: cfg, Ctx: ctx,
		FindTools: func(string) (dump.Tools, error) { return dump.Tools{Dumper: os.Args[0], Client: os.Args[0]}, nil }}
	c.Pool = transfer.NewPool(c.Push, c.Settle, []time.Duration{time.Millisecond, time.Millisecond, time.Millisecond})
	t.Cleanup(func() { cancel(); c.Pool.Close() })
	return c
}
```

Add imports `os`, `config`, `dump`, `inventory`, `transfer`.

- [ ] **Step 2: Write the failing tests**

`coord/backup_test.go`:

```go
package coord

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/dump"
	"github.com/jufianto/sevjala/dump/dumptest"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/store"
)

func pairNode(t *testing.T, ctrl *Controller, n testAgent) {
	t.Helper()
	ctx := context.Background()
	if _, err := ctrl.AddAgent(ctx, n.lan.Addr()); err != nil {
		t.Fatal(err)
	}
	code, _, _ := n.codes.Issue()
	if _, err := ctrl.Pair(ctx, n.id.AgentID, code, n.id.FP); err != nil {
		t.Fatal(err)
	}
}

func waitState(t *testing.T, ctrl *Controller, id string, done func(localapi.Backup) bool) localapi.Backup {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := ctrl.Backup(context.Background(), id)
		if err == nil && done(b) {
			return b
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup %s: %+v, %v", id, b, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func state(s string) func(localapi.Backup) bool {
	return func(b localapi.Backup) bool { return b.State == s }
}

func errCode(err error) int {
	var e *localapi.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

func TestBackupNowReachesThreeNodes(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	var nodes []testAgent
	for _, h := range []string{"PC-1", "PC-2", "PC-3"} {
		n := startAgent(t, "node", h)
		pairNode(t, ctrl, n)
		nodes = append(nodes, n)
	}
	jobs, err := ctrl.BackupNow(context.Background(), "", nil)
	if err != nil || len(jobs) != 1 || jobs[0].Database != "shop" {
		t.Fatalf("jobs %+v, %v", jobs, err)
	}
	b := waitState(t, ctrl, jobs[0].BackupID, state(store.BackupDistributed))
	if len(b.Copies) != 3 || !slices.Equal(b.Tags, []string{"manual"}) {
		t.Fatalf("backup %+v", b)
	}
	for _, c := range b.Copies {
		if c.State != store.CopyVerified || c.VerifiedAt.IsZero() || !strings.HasPrefix(c.Hostname, "PC-") {
			t.Errorf("copy %+v", c)
		}
	}
	for _, n := range nodes {
		inv, _ := n.inv.Scan()
		if len(inv.Backups) != 1 || inv.Backups[0].SHA256 != b.SHA256 || inv.Backups[0].State != inventory.StateVerified {
			t.Errorf("node store %+v", inv)
		}
	}
	for _, ext := range []string{inventory.DataExt, inventory.ManifestExt} {
		if _, err := os.Stat(filepath.Join(ctrl.Staging, b.BackupID+ext)); err != nil {
			t.Errorf("staging: %v", err)
		}
	}
}

func TestBackupNowWithoutNodesStaysInStaging(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	jobs, err := ctrl.BackupNow(context.Background(), "shop", []string{"daily"})
	if err != nil {
		t.Fatal(err)
	}
	b := waitState(t, ctrl, jobs[0].BackupID, state(store.BackupCreated))
	if len(b.Copies) != 0 || !slices.Equal(b.Tags, []string{"daily"}) {
		t.Errorf("backup %+v", b)
	}
}

func TestBackupNowRefuses(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	tests := []struct {
		name string
		dbs  []string
		db   string
		tags []string
		want string
	}{
		{"nothing configured", nil, "", nil, "no database is configured"},
		{"unknown database", []string{"shop"}, "other", nil, "not a configured database"},
		{"bad tag", []string{"shop"}, "", []string{"Daily Run"}, "tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := newController(t)
			ctrl.Config.Controller.Database.Databases = tt.dbs
			_, err := ctrl.BackupNow(context.Background(), tt.db, tt.tags)
			if errCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

func TestBackupNowWhileDumpingIs409(t *testing.T) {
	dumptest.Use(t, dumptest.Hang)
	ctrl := newController(t)
	jobs, err := ctrl.BackupNow(context.Background(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, ctrl, jobs[0].BackupID, state(localapi.BackupDumping))
	if _, err := ctrl.BackupNow(context.Background(), "", nil); errCode(err) != http.StatusConflict {
		t.Errorf("err = %v", err)
	}
}

func TestBackupDumpFailureIsReported(t *testing.T) {
	dumptest.Use(t, dumptest.DumpFails)
	ctrl := newController(t)
	jobs, _ := ctrl.BackupNow(context.Background(), "", nil)
	b := waitState(t, ctrl, jobs[0].BackupID, state(localapi.BackupFailed))
	if !strings.Contains(b.Error, "Unknown database") {
		t.Errorf("error %q", b.Error)
	}
	if all, _ := ctrl.Backups(context.Background()); len(all) != 1 || all[0].State != localapi.BackupFailed {
		t.Errorf("list %+v", all)
	}
}

func TestPushToStoppedNodeEndsOffline(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-OFF")
	pairNode(t, ctrl, n)
	n.lan.Shutdown(context.Background())
	jobs, _ := ctrl.BackupNow(context.Background(), "", nil)
	b := waitState(t, ctrl, jobs[0].BackupID, state(store.BackupPartial))
	if c := b.Copies[0]; c.State != store.CopyOffline || c.Attempts != 4 || c.LastError == "" {
		t.Errorf("copy %+v", c)
	}
}

// A node re-paired to another controller refuses us at TLS; retrying for
// twelve minutes cannot fix that.
func TestPushToRepairedNodeFailsAtOnce(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-MOVED")
	pairNode(t, ctrl, n)
	pairNode(t, newController(t), n)
	jobs, _ := ctrl.BackupNow(context.Background(), "", nil)
	b := waitState(t, ctrl, jobs[0].BackupID, state(store.BackupPartial))
	if c := b.Copies[0]; c.State != store.CopyFailed || c.Attempts != 1 {
		t.Errorf("copy %+v", c)
	}
}

func TestResumeRequeuesUnsettled(t *testing.T) {
	bin, _ := dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	n := startAgent(t, "node", "PC-R")
	pairNode(t, ctrl, n)
	ctx := context.Background()
	m, err := dump.Run(ctx, dump.Options{ID: inventory.NewID(time.Now()), Database: "shop", StagingDir: ctrl.Staging,
		SourceAgent: ctrl.Self.AgentID, Created: time.Now(), Tools: dump.Tools{Dumper: bin, Client: bin},
		Conn: dump.Conn{Host: "127.0.0.1", Port: 3306, User: "root"}})
	if err != nil {
		t.Fatal(err)
	}
	b := store.Backup{BackupID: m.BackupID, Database: m.Database, CreatedAt: m.CreatedAt, Size: m.Size, SHA256: m.SHA256,
		StagingPath: m.BackupID + inventory.DataExt}
	if err := ctrl.Store.InsertBackup(ctx, b, []string{n.id.AgentID}); err != nil {
		t.Fatal(err)
	}
	_ = ctrl.Store.SetCopy(ctx, store.Copy{BackupID: m.BackupID, AgentID: n.id.AgentID, State: store.CopyTransferring, Attempts: 1})
	if err := ctrl.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	waitState(t, ctrl, m.BackupID, state(store.BackupDistributed))
}

func TestSetDatabaseSavesWithoutThePassword(t *testing.T) {
	dumptest.Use(t, dumptest.OK)
	ctrl := newController(t)
	pw, dbs := "p a$s\"wörd", []string{"shop", "legacy"}
	d, err := ctrl.SetDatabase(context.Background(), localapi.DBSettings{Password: &pw, Databases: dbs})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Databases) != 2 || !d.Databases[0].Configured || !d.Databases[1].Configured || d.Databases[0].Consistency != "--lock-tables" {
		t.Errorf("detection %+v", d)
	}
	raw, err := os.ReadFile(config.Path(ctrl.DataDir))
	if err != nil || strings.Contains(string(raw), "wörd") || !strings.Contains(string(raw), `password_ref = "keyring:db"`) {
		t.Errorf("config.toml %q, %v", raw, err)
	}
	if got, err := config.Secret(ctrl.DataDir, "keyring:db"); err != nil || got != pw {
		t.Errorf("secret %q, %v", got, err)
	}
	empty := ""
	if _, err := ctrl.SetDatabase(context.Background(), localapi.DBSettings{Password: &empty}); err != nil {
		t.Fatal(err)
	}
	if ctrl.config().Controller.Database.PasswordRef != "" {
		t.Error("password_ref kept after clearing the password")
	}
}

func TestSetDatabaseRefusesBeforeSaving(t *testing.T) {
	tests := []struct {
		name, mode string
		s          localapi.DBSettings
		want       string
	}{
		{"unknown database", dumptest.OK, localapi.DBSettings{Databases: []string{"nope"}}, "does not exist"},
		{"login denied", dumptest.Denied, localapi.DBSettings{Databases: []string{"shop"}}, "Access denied"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dumptest.Use(t, tt.mode)
			ctrl := newController(t)
			if _, err := ctrl.SetDatabase(context.Background(), tt.s); errCode(err) != http.StatusBadRequest || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("err = %v", err)
			}
			if _, err := os.Stat(config.Path(ctrl.DataDir)); !errors.Is(err, os.ErrNotExist) {
				t.Error("config written")
			}
		})
	}
}
```

Run: `go test ./coord/` → Expected: FAIL (`unknown field DataDir`, undefined `BackupNow` …).

- [ ] **Step 3: Extend the Controller struct**

In `coord/controller.go`:

```go
type Controller struct {
	Self      *identity.Identity
	Store     *store.Store
	LAN       *lanapi.Client
	Targets   func() ([]*net.UDPAddr, error)
	Wait      time.Duration
	Log       *slog.Logger
	DataDir   string
	Staging   string
	Config    config.Config   // after start, read with config() and replaced by SetDatabase
	Ctx       context.Context // the agent's lifetime: dumps outlive the request that started them
	FindTools func(dumper string) (dump.Tools, error)
	Pool      *transfer.Pool

	mu      sync.Mutex
	seen    map[string]localapi.Discovered // from discover and agents add; pair needs the fp seen here
	jobs    map[string]*job                // dumps not yet in the store
	running map[string]string              // database → backup_id being dumped

	cfgMu sync.Mutex
}

func (c *Controller) config() config.Config {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	return c.Config
}
```

- [ ] **Step 4: Implement `coord/backup.go`**

```go
package coord

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/dump"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/store"
	"github.com/jufianto/sevjala/transfer"
)

const defaultTag = "manual"

var tagPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// job is a dump in progress or a failed one; it leaves memory once the
// backup is in the store.
type job struct {
	id, db string
	bytes  atomic.Int64
	err    string // guarded by Controller.mu
}

func (c *Controller) BackupNow(_ context.Context, db string, tags []string) ([]localapi.BackupJob, error) {
	cfg := c.config()
	dbs := cfg.Controller.Database.Databases
	if len(dbs) == 0 {
		return nil, localapi.Errorf(http.StatusBadRequest, "no database is configured; run 'sevjala db detect', then 'sevjala db set --db <name>'")
	}
	if db != "" {
		if !slices.Contains(dbs, db) {
			return nil, localapi.Errorf(http.StatusBadRequest, "%s is not a configured database (configured: %s)", db, strings.Join(dbs, ", "))
		}
		dbs = []string{db}
	}
	if len(tags) == 0 {
		tags = []string{defaultTag}
	}
	for _, t := range tags {
		if !tagPattern.MatchString(t) {
			return nil, localapi.Errorf(http.StatusBadRequest, "tag %q: use lower-case letters, digits, '-' and '_'", t)
		}
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, name := range dbs {
		if id, busy := c.running[name]; busy {
			return nil, localapi.Errorf(http.StatusConflict, "a backup of %s is already running (%s)", name, id)
		}
	}
	if c.jobs == nil {
		c.jobs, c.running = map[string]*job{}, map[string]string{}
	}
	out := make([]localapi.BackupJob, len(dbs))
	queued := make([]*job, len(dbs))
	for i, name := range dbs {
		id := inventory.NewID(now)
		for c.jobs[id] != nil {
			id = inventory.NewID(now)
		}
		j := &job{id: id, db: name}
		c.jobs[id], c.running[name] = j, id
		out[i], queued[i] = localapi.BackupJob{BackupID: id, Database: name}, j
	}
	go func() {
		for _, j := range queued {
			c.runBackup(j, now, tags, cfg)
		}
	}()
	return out, nil
}

func (c *Controller) runBackup(j *job, created time.Time, tags []string, cfg config.Config) {
	defer func() {
		c.mu.Lock()
		delete(c.running, j.db)
		c.mu.Unlock()
	}()
	log := c.Log.With("backup_id", j.id, "database", j.db)
	log.Info("backup started", logx.Event(logx.EventBackupStarted))
	m, err := c.dump(j, created, tags, cfg)
	var nodes []string
	if err == nil {
		nodes, err = c.nodeIDs(c.Ctx)
	}
	if err == nil {
		err = c.Store.InsertBackup(c.Ctx, store.Backup{BackupID: m.BackupID, Database: m.Database, CreatedAt: m.CreatedAt,
			Size: m.Size, SHA256: m.SHA256, Tags: m.Tags, StagingPath: m.BackupID + inventory.DataExt}, nodes)
	}
	c.mu.Lock()
	if err != nil {
		j.err = err.Error()
	} else {
		delete(c.jobs, j.id)
	}
	c.mu.Unlock()
	if err != nil {
		log.Error("backup failed", logx.Event(logx.EventBackupFailed), "err", err)
		return
	}
	log.Info("backup created", logx.Event(logx.EventBackupCreated), "size", m.Size, "sha256", m.SHA256, "nodes", len(nodes))
	for _, n := range nodes {
		c.Pool.Enqueue(transfer.Job{BackupID: m.BackupID, AgentID: n})
	}
}

func (c *Controller) dump(j *job, created time.Time, tags []string, cfg config.Config) (inventory.Manifest, error) {
	db := cfg.Controller.Database
	pw, err := config.Secret(c.DataDir, db.PasswordRef)
	if err != nil {
		return inventory.Manifest{}, fmt.Errorf("database password: %w", err)
	}
	tools, err := c.FindTools(db.Dumper)
	if err != nil {
		return inventory.Manifest{}, err
	}
	return dump.Run(c.Ctx, dump.Options{ID: j.id, Database: j.db, StagingDir: c.Staging, SourceAgent: c.Self.AgentID,
		Created: created, Tags: tags, Tools: tools, Conn: dump.Conn{Host: db.Host, Port: db.Port, User: db.User, Password: pw},
		Progress: func(n int64) { j.bytes.Store(n) }})
}

func (c *Controller) nodeIDs(ctx context.Context) ([]string, error) {
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, a := range as {
		if a.Role == config.RoleNode {
			out = append(out, a.AgentID)
		}
	}
	return out, nil
}

// Push sends one staged backup to one node; the transfer pool calls it and
// retries what it returns unless it is marked Permanent.
func (c *Controller) Push(ctx context.Context, j transfer.Job) error {
	a, err := c.Store.Agent(ctx, j.AgentID)
	if err != nil {
		return transfer.Permanent(err)
	}
	m, err := inventory.ReadManifest(filepath.Join(c.Staging, j.BackupID+inventory.ManifestExt))
	if err != nil {
		return transfer.Permanent(fmt.Errorf("staging copy: %w", err))
	}
	f, err := os.Open(filepath.Join(c.Staging, j.BackupID+inventory.DataExt))
	if err != nil {
		return transfer.Permanent(fmt.Errorf("staging copy: %w", err))
	}
	defer f.Close()
	addr := net.JoinHostPort(a.LastIP, strconv.Itoa(a.Port))
	c.Log.Info("transfer started", logx.Event(logx.EventTransferStarted), "backup_id", j.BackupID, "agent_id", j.AgentID, "addr", addr)
	err = c.LAN.PutBackup(ctx, addr, a.FP, m, f)
	var he *lanapi.HTTPError
	var op *net.OpError
	switch {
	case err == nil:
		return nil
	case errors.Is(err, lanapi.ErrRejected):
		return transfer.Permanent(err)
	case errors.As(err, &he) && (he.Code == http.StatusBadRequest || he.Code == http.StatusForbidden || he.Code == http.StatusConflict):
		return transfer.Permanent(err)
	case errors.As(err, &op) && op.Op == "dial":
		return transfer.Unreachable(err)
	}
	return err
}

// Settle records what the transfer pool reports.
func (c *Controller) Settle(j transfer.Job, state string, attempts int, err error) {
	cp := store.Copy{BackupID: j.BackupID, AgentID: j.AgentID, State: state, Attempts: attempts}
	log := c.Log.With("backup_id", j.BackupID, "agent_id", j.AgentID, "attempt", attempts)
	switch {
	case state == store.CopyVerified:
		cp.VerifiedAt = time.Now().UTC().Truncate(time.Second)
		log.Info("transfer verified", logx.Event(logx.EventTransferVerified))
	case err != nil:
		cp.LastError = err.Error()
		log.Warn("transfer failed", logx.Event(logx.EventTransferFailed), "state", state, "err", err)
	}
	if err := c.Store.SetCopy(context.Background(), cp); err != nil {
		c.Log.Error("record transfer state", "backup_id", j.BackupID, "agent_id", j.AgentID, "err", err)
	}
}

// Resume requeues the copies a restart interrupted.
func (c *Controller) Resume(ctx context.Context) error {
	cs, err := c.Store.Unsettled(ctx)
	if err != nil {
		return err
	}
	for _, cp := range cs {
		c.Pool.Enqueue(transfer.Job{BackupID: cp.BackupID, AgentID: cp.AgentID})
	}
	return nil
}

func (c *Controller) Backups(ctx context.Context) ([]localapi.Backup, error) {
	bs, err := c.Store.Backups(ctx)
	if err != nil {
		return nil, err
	}
	hosts, err := c.hostnames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]localapi.Backup, 0, len(bs))
	stored := map[string]bool{}
	for _, b := range bs {
		out = append(out, toBackup(b, hosts))
		stored[b.BackupID] = true
	}
	c.mu.Lock()
	for _, j := range c.jobs {
		if !stored[j.id] {
			out = append(out, j.view())
		}
	}
	c.mu.Unlock()
	slices.SortFunc(out, func(a, b localapi.Backup) int { return strings.Compare(b.BackupID, a.BackupID) })
	return out, nil
}

func (c *Controller) Backup(ctx context.Context, id string) (localapi.Backup, error) {
	b, err := c.Store.Backup(ctx, id)
	if err == nil {
		hosts, err := c.hostnames(ctx)
		return toBackup(b, hosts), err
	}
	if !errors.Is(err, store.ErrNotFound) {
		return localapi.Backup{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if j := c.jobs[id]; j != nil {
		return j.view(), nil
	}
	return localapi.Backup{}, localapi.Errorf(http.StatusNotFound, "no backup %s", id)
}

// view must be called with Controller.mu held.
func (j *job) view() localapi.Backup {
	b := localapi.Backup{BackupID: j.id, Database: j.db, State: localapi.BackupDumping, Bytes: j.bytes.Load(),
		Tags: []string{}, Copies: []localapi.BackupCopy{}}
	if j.err != "" {
		b.State, b.Error = localapi.BackupFailed, j.err
	}
	return b
}

func (c *Controller) hostnames(ctx context.Context) (map[string]string, error) {
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(as))
	for _, a := range as {
		out[a.AgentID] = a.Hostname
	}
	return out, nil
}

func toBackup(b store.Backup, hosts map[string]string) localapi.Backup {
	out := localapi.Backup{BackupID: b.BackupID, Database: b.Database, CreatedAt: b.CreatedAt, Size: b.Size, SHA256: b.SHA256,
		Tags: b.Tags, State: b.State, Copies: make([]localapi.BackupCopy, len(b.Copies))}
	for i, cp := range b.Copies {
		host := hosts[cp.AgentID]
		if host == "" {
			host = cp.AgentID
		}
		out.Copies[i] = localapi.BackupCopy{AgentID: cp.AgentID, Hostname: host, State: cp.State, Attempts: cp.Attempts,
			LastError: cp.LastError, VerifiedAt: cp.VerifiedAt}
	}
	return out
}
```

- [ ] **Step 5: Implement `coord/database.go`**

```go
package coord

import (
	"context"
	"net/http"
	"slices"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/dump"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
)

const dbSecret = "db"

func (c *Controller) DetectDatabase(ctx context.Context) (localapi.Detection, error) {
	return c.detect(ctx, c.config().Controller.Database, nil)
}

// detect uses password when given, else the stored secret.
func (c *Controller) detect(ctx context.Context, db config.Database, password *string) (localapi.Detection, error) {
	tools, err := c.FindTools(db.Dumper)
	if err != nil {
		return localapi.Detection{}, localapi.Errorf(http.StatusBadRequest, "%v", err)
	}
	var pw string
	if password != nil {
		pw = *password
	} else if pw, err = config.Secret(c.DataDir, db.PasswordRef); err != nil {
		return localapi.Detection{}, localapi.Errorf(http.StatusInternalServerError, "database password: %v", err)
	}
	d, err := dump.Detect(ctx, tools, dump.Conn{Host: db.Host, Port: db.Port, User: db.User, Password: pw})
	if err != nil {
		return localapi.Detection{}, localapi.Errorf(http.StatusBadRequest, "%v", err)
	}
	out := localapi.Detection{Dumper: tools.Dumper, Client: tools.Client, ServerVersion: d.ServerVersion, Flavour: d.Flavour,
		Databases: make([]localapi.DBDatabase, len(d.Databases))}
	for i, x := range d.Databases {
		out.Databases[i] = localapi.DBDatabase{Name: x.Name, Tables: x.Tables, Consistency: x.Consistency(),
			NonInnoDB: x.NonInnoDB, Configured: slices.Contains(db.Databases, x.Name)}
	}
	return out, nil
}

// SetDatabase tests the new settings against the server before it saves
// anything: config.toml, and the password in the keyring.
func (c *Controller) SetDatabase(ctx context.Context, s localapi.DBSettings) (localapi.Detection, error) {
	c.cfgMu.Lock()
	defer c.cfgMu.Unlock()
	cfg := c.Config
	db := &cfg.Controller.Database
	if s.Host != nil {
		db.Host = *s.Host
	}
	if s.Port != nil {
		db.Port = *s.Port
	}
	if s.User != nil {
		db.User = *s.User
	}
	if s.Dumper != nil {
		db.Dumper = *s.Dumper
	}
	if s.Databases != nil {
		db.Databases = slices.Compact(slices.Sorted(slices.Values(s.Databases)))
	}
	if err := cfg.Validate(); err != nil {
		return localapi.Detection{}, localapi.Errorf(http.StatusBadRequest, "%v", err)
	}
	if db.Host == "" || db.User == "" {
		return localapi.Detection{}, localapi.Errorf(http.StatusBadRequest, "host and user must not be empty")
	}
	d, err := c.detect(ctx, *db, s.Password)
	if err != nil {
		return localapi.Detection{}, err
	}
	for _, name := range db.Databases {
		if !slices.ContainsFunc(d.Databases, func(x localapi.DBDatabase) bool { return x.Name == name }) {
			return localapi.Detection{}, localapi.Errorf(http.StatusBadRequest, "database %s does not exist on %s:%d", name, db.Host, db.Port)
		}
	}
	if s.Password != nil {
		if *s.Password == "" {
			if err := config.DeleteSecret(c.DataDir, dbSecret); err != nil {
				return localapi.Detection{}, err
			}
			db.PasswordRef = ""
		} else {
			fallback, err := config.SetSecret(c.DataDir, dbSecret, *s.Password)
			if err != nil {
				return localapi.Detection{}, err
			}
			if fallback {
				c.Log.Warn("no OS keyring; the database password is in an encrypted file", logx.Event(logx.EventKeyringFallback))
			}
			db.PasswordRef = config.KeyringPrefix + dbSecret
		}
	}
	if err := config.Write(config.Path(c.DataDir), cfg); err != nil {
		return localapi.Detection{}, err
	}
	c.Config = cfg
	for i := range d.Databases {
		d.Databases[i].Configured = slices.Contains(db.Databases, d.Databases[i].Name)
	}
	c.Log.Info("database configured", logx.Event(logx.EventDBConfigured), "host", db.Host, "port", db.Port, "user", db.User,
		"databases", db.Databases)
	return d, nil
}
```

`SetDatabase` holds `cfgMu`, so it reads `c.Config` directly; `config()` would deadlock. `BackupNow` calls `config()` from another goroutine, which is safe.

- [ ] **Step 6: Add the events**

In `logx/logx.go`, add:

```go
	EventBackupStarted    = "backup_started"
	EventBackupCreated    = "backup_created"
	EventBackupFailed     = "backup_failed"
	EventTransferStarted  = "transfer_started"
	EventTransferVerified = "transfer_verified"
	EventTransferFailed   = "transfer_failed"
	EventDBConfigured     = "db_configured"
	EventKeyringFallback  = "keyring_fallback"
```

- [ ] **Step 7: Run the tests**

Run: `go test -race ./coord/`
Expected: PASS. `go vet ./...` may flag `copylocks` if a `Controller` is copied anywhere; it is only used through pointers.

- [ ] **Step 8: Commit**

```bash
git add coord logx
git commit -m "feat(coord): backup now dumps, records and pushes to every paired node; db detect/set through the agent"
```

---

### Task 10: Local API routes for M2

**Files:**
- Modify: `localapi/api.go` (`Backend`), `localapi/server.go`, `localapi/client.go`, `localapi/localapi_test.go`, `coord/coord.go`, `coord/coord_test.go`

**Interfaces:**
- Consumes: the Task 9 `Controller` methods.
- Produces:
  - `Backend` methods:
    - `DetectDatabase(ctx) (Detection, error)`
    - `SetDatabase(ctx, DBSettings) (Detection, error)`
    - `BackupNow(ctx, db string, tags []string) ([]BackupJob, error)`
    - `Backups(ctx) ([]Backup, error)`
    - `Backup(ctx, id string) (Backup, error)`
  - `Client` methods with the same names and signatures.
  - Routes `POST /db/detect`, `PUT /db`, `POST /backup/now`, `GET /backups`, `GET /backups/{id}`.

- [ ] **Step 1: Write the failing round-trip test**

In `localapi/localapi_test.go`, extend `fakeBackend`:

```go
	gotSettings DBSettings
	gotDB       string
	gotTags     []string
```

```go
func (f *fakeBackend) DetectDatabase(context.Context) (Detection, error) {
	return Detection{Dumper: "/x/mysqldump", Databases: []DBDatabase{{Name: "shop", Configured: true}}}, f.err
}

func (f *fakeBackend) SetDatabase(_ context.Context, s DBSettings) (Detection, error) {
	f.mu.Lock()
	f.gotSettings = s
	f.mu.Unlock()
	return Detection{Dumper: "/x/mysqldump"}, f.err
}

func (f *fakeBackend) BackupNow(_ context.Context, db string, tags []string) ([]BackupJob, error) {
	f.mu.Lock()
	f.gotDB, f.gotTags = db, tags
	f.mu.Unlock()
	return []BackupJob{{BackupID: "BK-20261013-101500-A8F2", Database: "shop"}}, f.err
}

func (f *fakeBackend) Backups(context.Context) ([]Backup, error) { return nil, f.err }

func (f *fakeBackend) Backup(_ context.Context, id string) (Backup, error) {
	return Backup{BackupID: id, State: BackupDumping, Bytes: 7}, f.err
}
```

Add the test:

```go
func TestBackupRoutesRoundTrip(t *testing.T) {
	f := &fakeBackend{}
	c := NewClient(startBackend(t, f))
	ctx := context.Background()
	if d, err := c.DetectDatabase(ctx); err != nil || d.Dumper != "/x/mysqldump" || !d.Databases[0].Configured {
		t.Errorf("DetectDatabase %+v, %v", d, err)
	}
	pw, port := "s3cret", 3307
	if _, err := c.SetDatabase(ctx, DBSettings{Password: &pw, Port: &port, Databases: []string{}}); err != nil {
		t.Fatal(err)
	}
	if s := f.gotSettings; s.Password == nil || *s.Password != pw || *s.Port != port || s.Host != nil || s.Databases == nil {
		t.Errorf("settings %+v", s)
	}
	if _, err := c.SetDatabase(ctx, DBSettings{}); err != nil || f.gotSettings.Databases != nil {
		t.Errorf("nil databases must stay nil: %+v, %v", f.gotSettings, err)
	}
	jobs, err := c.BackupNow(ctx, "shop", []string{"daily"})
	if err != nil || len(jobs) != 1 || f.gotDB != "shop" || !slices.Equal(f.gotTags, []string{"daily"}) {
		t.Errorf("BackupNow %+v, %v, %q %q", jobs, err, f.gotDB, f.gotTags)
	}
	if bs, err := c.Backups(ctx); err != nil || bs == nil || len(bs) != 0 {
		t.Errorf("Backups %+v, %v", bs, err)
	}
	if b, err := c.Backup(ctx, "BK-20261013-101500-A8F2"); err != nil || b.State != BackupDumping || b.Bytes != 7 {
		t.Errorf("Backup %+v, %v", b, err)
	}
}
```

Add `slices` to the imports.

Run: `go test ./localapi/` → Expected: FAIL (`c.DetectDatabase undefined`).

- [ ] **Step 2: Implement the interface, routes and client**

`localapi/api.go`, inside `Backend`:

```go
	DetectDatabase(ctx context.Context) (Detection, error)
	SetDatabase(ctx context.Context, s DBSettings) (Detection, error)
	BackupNow(ctx context.Context, db string, tags []string) ([]BackupJob, error)
	Backups(ctx context.Context) ([]Backup, error)
	Backup(ctx context.Context, id string) (Backup, error)
```

Below `listOf`:

```go
type backupJobs struct {
	Backups []BackupJob `json:"backups"`
}

type backupList struct {
	Backups []Backup `json:"backups"`
}
```

`localapi/server.go`, in `NewHandler` before the catch-all:

```go
	mux.HandleFunc("POST /db/detect", func(w http.ResponseWriter, r *http.Request) {
		d, err := b.DetectDatabase(r.Context())
		reply(w, d, err)
	})
	mux.HandleFunc("PUT /db", func(w http.ResponseWriter, r *http.Request) {
		var s DBSettings
		if decode(w, r, &s) {
			d, err := b.SetDatabase(r.Context(), s)
			reply(w, d, err)
		}
	})
	mux.HandleFunc("POST /backup/now", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DB   string   `json:"db"`
			Tags []string `json:"tags"`
		}
		if decode(w, r, &req) {
			js, err := b.BackupNow(r.Context(), req.DB, req.Tags)
			reply(w, backupJobs{Backups: js}, err)
		}
	})
	mux.HandleFunc("GET /backups", func(w http.ResponseWriter, r *http.Request) {
		bs, err := b.Backups(r.Context())
		if bs == nil {
			bs = []Backup{}
		}
		reply(w, backupList{Backups: bs}, err)
	})
	mux.HandleFunc("GET /backups/{id}", func(w http.ResponseWriter, r *http.Request) {
		bk, err := b.Backup(r.Context(), r.PathValue("id"))
		reply(w, bk, err)
	})
```

`localapi/client.go`:

```go
func (c *Client) DetectDatabase(ctx context.Context) (Detection, error) {
	var d Detection
	return d, c.do(ctx, http.MethodPost, "/db/detect", nil, &d)
}

func (c *Client) SetDatabase(ctx context.Context, s DBSettings) (Detection, error) {
	var d Detection
	return d, c.do(ctx, http.MethodPut, "/db", s, &d)
}

func (c *Client) BackupNow(ctx context.Context, db string, tags []string) ([]BackupJob, error) {
	var out backupJobs
	err := c.do(ctx, http.MethodPost, "/backup/now", map[string]any{"db": db, "tags": tags}, &out)
	return out.Backups, err
}

func (c *Client) Backups(ctx context.Context) ([]Backup, error) {
	var out backupList
	err := c.do(ctx, http.MethodGet, "/backups", nil, &out)
	return out.Backups, err
}

func (c *Client) Backup(ctx context.Context, id string) (Backup, error) {
	var b Backup
	return b, c.do(ctx, http.MethodGet, "/backups/"+url.PathEscape(id), nil, &b)
}
```

`coord/coord.go`, `Service` passthroughs:

```go
func (s *Service) DetectDatabase(ctx context.Context) (localapi.Detection, error) {
	if s.Controller == nil {
		return localapi.Detection{}, errNotController
	}
	return s.Controller.DetectDatabase(ctx)
}

func (s *Service) SetDatabase(ctx context.Context, set localapi.DBSettings) (localapi.Detection, error) {
	if s.Controller == nil {
		return localapi.Detection{}, errNotController
	}
	return s.Controller.SetDatabase(ctx, set)
}

func (s *Service) BackupNow(ctx context.Context, db string, tags []string) ([]localapi.BackupJob, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	return s.Controller.BackupNow(ctx, db, tags)
}

func (s *Service) Backups(ctx context.Context) ([]localapi.Backup, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	return s.Controller.Backups(ctx)
}

func (s *Service) Backup(ctx context.Context, id string) (localapi.Backup, error) {
	if s.Controller == nil {
		return localapi.Backup{}, errNotController
	}
	return s.Controller.Backup(ctx, id)
}
```

In `coord/coord_test.go` `TestWrongRoleIs409`, add `node.BackupNow(context.Background(), "", nil)` and `node.DetectDatabase(context.Background())`, each expecting `is409(err, "this agent is a node")`.

- [ ] **Step 3: Run the tests**

Run: `go test -race ./localapi/ ./coord/ && go build ./...`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add localapi coord
git commit -m "feat(localapi): db detect/set, backup now and backups routes"
```

---

### Task 11: Agent wiring (`svc`)

**Files:**
- Modify: `svc/agent.go`, `svc/agent_test.go`

**Interfaces:**
- Consumes:
  - `inventory.Open`, `dump.CleanStaging`, `dump.FindTools`
  - `transfer.NewPool`, `transfer.DefaultDelays`
  - `config.StagingDir`, `Config.StorePath`
  - The `Controller` fields from Task 9
  - `lanapi.Options.Store`, `localapi.StoreInfo`
- Produces: a node agent serves the store, and a controller agent cleans staging, runs the pool and resumes copies.

- [ ] **Step 1: Write the failing tests**

Append to `svc/agent_test.go`:

```go
func TestNodeAgentServesItsStore(t *testing.T) {
	dir := shortDir(t)
	a := &Agent{DataDir: dir, Config: testConfig(config.RoleNode), Version: "test"}
	if err := a.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(nil)
	st, err := localapi.NewClient(localapi.Endpoint(dir)).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Store == nil || st.Store.Path != filepath.Join(dir, "store") || st.Store.FreeBytes <= 0 {
		t.Errorf("store %+v", st.Store)
	}
}

func TestControllerStartRemovesUnfinishedDumps(t *testing.T) {
	dir := shortDir(t)
	staging := filepath.Join(dir, "staging")
	if err := os.MkdirAll(staging, 0o700); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(staging, "BK-20261013-101500-A8F2.part")
	_ = os.WriteFile(part, []byte("x"), 0o600)
	a := &Agent{DataDir: dir, Config: testConfig(config.RoleController), Version: "test"}
	if err := a.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(nil)
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Errorf("unfinished dump kept: %v", err)
	}
	bs, err := localapi.NewClient(localapi.Endpoint(dir)).Backups(context.Background())
	if err != nil || len(bs) != 0 {
		t.Errorf("backups %+v, %v", bs, err)
	}
}
```

Run: `go test ./svc/` → Expected: FAIL (`st.Store` nil / `.part` kept).

- [ ] **Step 2: Implement the wiring**

In `svc/agent.go`, `Agent` gains:

```go
	inv    *inventory.Store
	pool   *transfer.Pool
	cancel context.CancelFunc
```

In `start()`, replace the role switch and what follows it:

```go
	var codes *pairing.Codes
	switch a.Config.Role {
	case config.RoleNode:
		if a.inv, err = inventory.Open(a.Config.StorePath(a.DataDir), a.Config.Node.Store.MinFreeBytes, a.log); err != nil {
			return err
		}
		codes = pairing.New(time.Now)
		backend.Node = &coord.Node{AgentID: id.AgentID, FP: id.FP, Codes: codes, Log: a.log}
	case config.RoleController:
		if a.store, err = store.Open(filepath.Join(a.DataDir, store.FileName)); err != nil {
			return err
		}
		staging := config.StagingDir(a.DataDir)
		if err := os.MkdirAll(staging, 0o700); err != nil {
			return err
		}
		removed, err := dump.CleanStaging(staging)
		if err != nil {
			return err
		}
		for _, name := range removed {
			a.log.Warn("removed unfinished dump", logx.Event(logx.EventPartRemoved), "file", name)
		}
		ctx, cancel := context.WithCancel(context.Background())
		a.cancel = cancel
		port := a.Config.DiscoveryPort
		ctrl := &coord.Controller{Self: id, Store: a.store, LAN: lanapi.NewClient(id),
			Targets: func() ([]*net.UDPAddr, error) { return discovery.BroadcastTargets(port) },
			Wait:    discovery.DefaultWait, Log: a.log, DataDir: a.DataDir, Staging: staging, Config: a.Config,
			Ctx: ctx, FindTools: dump.FindTools}
		a.pool = transfer.NewPool(ctrl.Push, ctrl.Settle, transfer.DefaultDelays)
		ctrl.Pool = a.pool
		backend.Controller = ctrl
	}
	if a.api, err = localapi.Start(localapi.Endpoint(a.DataDir), localapi.NewHandler(backend), a.log); err != nil {
		return err
	}
	if a.lan, err = lanapi.Start(lanapi.Options{Addr: a.Config.Listen, DataDir: a.DataDir, Identity: id, Role: a.Config.Role,
		Version: a.Version, Hostname: hostname, Started: a.started, Codes: codes, Store: a.inv, Log: a.log}); err != nil {
		return err
	}
	if backend.Controller != nil {
		if err := backend.Controller.Resume(context.Background()); err != nil {
			return err
		}
	}
	// … the discovery responder lines, unchanged
```

At the top of `shutdown()`, before the discovery block:

```go
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	if a.pool != nil {
		a.pool.Close()
		a.pool = nil
	}
	a.inv = nil
```

In `status()`, before the return, build the struct into `st` and add:

```go
	if a.inv != nil {
		info := &localapi.StoreInfo{Path: a.inv.Dir(), Problems: []string{}}
		if inv, err := a.inv.Scan(); err != nil {
			info.Problems = append(info.Problems, err.Error())
		} else {
			info.Backups, info.Problems = len(inv.Backups), append(info.Problems, inv.Problems...)
		}
		if free, err := a.inv.FreeBytes(); err != nil {
			info.Problems = append(info.Problems, err.Error())
		} else {
			info.FreeBytes = free
		}
		st.Store = info
	}
	return st
```

Imports gain `dump`, `inventory` and `transfer`.

- [ ] **Step 3: Run the tests**

Run: `go test -race ./svc/ && GOOS=windows go vet ./svc/`
Expected: PASS, no vet output.

- [ ] **Step 4: Commit**

```bash
git add svc
git commit -m "feat(svc): node agents serve their store; controllers clean staging, run transfers and resume copies"
```

---

### Task 12: CLI (`db`, `backup now`, `backups list`, doctor)

**Files:**
- Create: `cmd/db.go`, `cmd/backup.go`, `cmd/db_test.go`, `cmd/backup_test.go`
- Modify: `cmd/root.go`, `cmd/doctor.go`, `cmd/doctor_test.go`, `go.mod`, `go.sum`

**Interfaces:**
- Consumes:
  - The local API client methods from Task 10.
  - `localapi.Status.Store`.
  - `config.StagingDir`.
- Produces:
  - Commands `sevjala db detect`, `sevjala db set`, `sevjala backup now`, `sevjala backups list [--matrix]`.
  - Doctor checks `staging`, `dumper`, `database` (controller) and `store` (node), with a `WARN` level.

- [ ] **Step 1: Add the dependency**

Run: `go get golang.org/x/term@latest`
Expected: `golang.org/x/term` in `go.mod` (it requires only `x/sys`).

- [ ] **Step 2: Write the failing CLI tests**

`cmd/db_test.go`:

```go
package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/localapi"
)

func parseSet(t *testing.T, args []string, stdin string) (localapi.DBSettings, error) {
	t.Helper()
	c, f := dbSetCmd(&globals{})
	if err := c.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return f.settings(c.Flags(), strings.NewReader(stdin), func() (string, error) { return "prompted", nil })
}

func TestDBSetFlags(t *testing.T) {
	s, err := parseSet(t, []string{"--port", "3307", "--db", "shop,legacy", "--password"}, "")
	if err != nil || *s.Port != 3307 || s.Host != nil || *s.Password != "prompted" || len(s.Databases) != 2 {
		t.Errorf("%+v, %v", s, err)
	}
	s, err = parseSet(t, []string{"--password-stdin"}, "p a$s\r\n")
	if err != nil || *s.Password != "p a$s" || s.Databases != nil {
		t.Errorf("%+v, %v", s, err)
	}
	s, err = parseSet(t, []string{"--no-password"}, "")
	if err != nil || s.Password == nil || *s.Password != "" {
		t.Errorf("%+v, %v", s, err)
	}
	for _, args := range [][]string{{"--password", "--no-password"}, {}, {"--password-stdin"}} {
		var ee *exitError
		if _, err := parseSet(t, args, ""); !errors.As(err, &ee) || ee.code != 2 {
			t.Errorf("%v: err = %v", args, err)
		}
	}
}

func TestRenderDetection(t *testing.T) {
	var b bytes.Buffer
	renderDetection(&b, localapi.Detection{Dumper: `C:\xampp\mysql\bin\mysqldump.exe`, Client: `C:\xampp\mysql\bin\mysql.exe`,
		ServerVersion: "10.4.32-MariaDB", Flavour: "MariaDB", Databases: []localapi.DBDatabase{
			{Name: "legacy", Tables: 2, NonInnoDB: []string{"posts (MyISAM)"}},
			{Name: "shop", Tables: 12, Configured: true}}})
	out := b.String()
	for _, want := range []string{"mysqldump.exe", "MariaDB 10.4.32-MariaDB", "lock-tables (not InnoDB: posts (MyISAM))", "single-transaction", "yes"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	if strings.Contains(out, "db set --db") {
		t.Errorf("hint shown although shop is configured:\n%s", out)
	}
}
```

`cmd/backup_test.go`:

```go
package cmd

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
)

func script(states ...localapi.Backup) func(context.Context, string) (localapi.Backup, error) {
	i := 0
	return func(context.Context, string) (localapi.Backup, error) {
		b := states[min(i, len(states)-1)]
		i++
		return b, nil
	}
}

func copies(states ...string) []localapi.BackupCopy {
	var out []localapi.BackupCopy
	for i, s := range states {
		out = append(out, localapi.BackupCopy{Hostname: "PC-" + string(rune('1'+i)), State: s})
	}
	return out
}

func TestWatchBackups(t *testing.T) {
	sha := strings.Repeat("a", 64)
	id := "BK-20261013-101500-A8F2"
	base := localapi.Backup{BackupID: id, Database: "shop", Size: 3 << 20, SHA256: sha}
	with := func(state string, cs []localapi.BackupCopy) localapi.Backup { b := base; b.State, b.Copies = state, cs; return b }
	tests := []struct {
		name   string
		steps  []localapi.Backup
		ok     bool
		output []string
	}{
		{"all verified", []localapi.Backup{
			{BackupID: id, Database: "shop", State: localapi.BackupDumping, Bytes: 1 << 20},
			with("created", copies("transferring", "pending")),
			with("distributed", copies("verified", "verified")),
		}, true, []string{"dumping… 1.0 MiB", "created 3.0 MiB sha256:aaaaaaaaaaaa", "PC-1 verified · PC-2 verified"}},
		{"dump failed", []localapi.Backup{{BackupID: id, Database: "shop", State: localapi.BackupFailed, Error: "Unknown database"}},
			false, []string{"dump failed: Unknown database"}},
		{"one copy failed", []localapi.Backup{
			with("partial", []localapi.BackupCopy{{Hostname: "PC-1", State: "verified"}, {Hostname: "PC-2", State: "failed", LastError: "422"}}),
		}, false, []string{"PC-2 failed (422)"}},
		{"no nodes", []localapi.Backup{with("created", nil)}, true, []string{"no paired nodes"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			ok, err := watchBackups(context.Background(), script(tt.steps...), []localapi.BackupJob{{BackupID: id}}, &b, time.Millisecond)
			if err != nil || ok != tt.ok {
				t.Fatalf("ok = %v, err = %v\n%s", ok, err, b.String())
			}
			for _, want := range tt.output {
				if !strings.Contains(b.String(), want) {
					t.Errorf("missing %q in\n%s", want, b.String())
				}
			}
		})
	}
}

func TestWatchStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var b bytes.Buffer
	ok, err := watchBackups(ctx, script(localapi.Backup{State: localapi.BackupDumping}), []localapi.BackupJob{{BackupID: "x"}}, &b, time.Hour)
	if ok || err != nil {
		t.Errorf("ok = %v, err = %v", ok, err)
	}
}

func TestRenderBackupsAndMatrix(t *testing.T) {
	bs := []localapi.Backup{{BackupID: "BK-20261013-101500-A8F2", Database: "shop", Size: 2048, Tags: []string{"manual"},
		CreatedAt: time.Date(2026, 10, 13, 10, 15, 0, 0, time.Local), State: "partial",
		Copies: []localapi.BackupCopy{{Hostname: "PC-1", State: "verified"}, {Hostname: "PC-2", State: "offline"}}}}
	var b bytes.Buffer
	renderBackups(&b, bs)
	if out := b.String(); !strings.Contains(out, "1/2 verified") || !strings.Contains(out, "2026-10-13 10:15") || !strings.Contains(out, "manual") {
		t.Errorf("list:\n%s", out)
	}
	b.Reset()
	renderMatrix(&b, bs, "linux")
	if out := b.String(); !strings.Contains(out, "PC-1") || !strings.Contains(out, "✓") || !strings.Contains(out, "○") {
		t.Errorf("matrix:\n%s", out)
	}
	b.Reset()
	renderMatrix(&b, bs, "windows")
	if out := b.String(); strings.ContainsAny(out, "✓○") || !strings.Contains(out, " v ") {
		t.Errorf("windows matrix:\n%s", out)
	}
	b.Reset()
	renderBackups(&b, nil)
	if !strings.Contains(b.String(), "sevjala backup now") {
		t.Errorf("empty list: %q", b.String())
	}
}
```

Append to `cmd/doctor_test.go`:

```go
func TestStoreCheck(t *testing.T) {
	tests := []struct {
		name     string
		s        *localapi.StoreInfo
		ok, warn bool
	}{
		{"healthy", &localapi.StoreInfo{Path: "/s", FreeBytes: 10 << 30}, true, false},
		{"under the floor", &localapi.StoreInfo{Path: "/s", FreeBytes: 1 << 30}, false, false},
		{"problems", &localapi.StoreInfo{Path: "/s", FreeBytes: 10 << 30, Problems: []string{"x: manifest without its backup file"}}, false, true},
		{"missing", nil, false, false},
	}
	for _, tt := range tests {
		if r := storeCheck(tt.s, 2<<30); r.OK != tt.ok || r.Warn != tt.warn {
			t.Errorf("%s: %+v", tt.name, r)
		}
	}
}

func TestDatabaseChecks(t *testing.T) {
	d := localapi.Detection{Dumper: "/x/mysqldump", ServerVersion: "10.4.32-MariaDB", Flavour: "MariaDB",
		Databases: []localapi.DBDatabase{{Name: "shop", Consistency: "--single-transaction", Configured: true}}}
	rs := databaseChecks(d, nil)
	if len(rs) != 2 || !rs[0].OK || !rs[1].OK || !strings.Contains(rs[1].Detail, "shop single-transaction") {
		t.Errorf("%+v", rs)
	}
	d.Databases[0].Configured = false
	if rs := databaseChecks(d, nil); !rs[1].Warn {
		t.Errorf("nothing configured: %+v", rs)
	}
	if rs := databaseChecks(localapi.Detection{}, errors.New("Access denied")); len(rs) != 1 || rs[0].OK || rs[0].Warn {
		t.Errorf("error: %+v", rs)
	}
}

func TestRenderChecksWarnDoesNotFail(t *testing.T) {
	var b bytes.Buffer
	if !renderChecks(&b, []checkResult{{Name: "a", OK: true}, {Name: "b", Warn: true, Detail: "hmm"}}) {
		t.Error("a warning failed doctor")
	}
	if !strings.Contains(b.String(), "WARN  b") {
		t.Errorf("%q", b.String())
	}
}
```

Rename `checkDataDir(x)` calls in `doctor_test.go` to `checkWritable("data dir", x)`.

Run: `go test ./cmd/` → Expected: FAIL (undefined `newDBSetCmd`, `watchBackups`, `storeCheck` …).

- [ ] **Step 3: Implement `cmd/db.go`**

```go
package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"github.com/jufianto/sevjala/localapi"
)

func newDBCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "db", Short: "Find and choose the database to back up (controller)"}
	c.AddCommand(&cobra.Command{
		Use:   "detect",
		Short: "Find mysqldump and list the databases it reaches",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			d, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Detection, error) {
				return cl.DetectDatabase(ctx)
			})
			if err != nil {
				return err
			}
			renderDetection(c.OutOrStdout(), d)
			return nil
		},
	}, newDBSetCmd(g))
	return c
}

type dbFlags struct {
	host, user, dumper                  string
	port                                int
	dbs                                 []string
	password, passwordStdin, noPassword bool
}

func newDBSetCmd(g *globals) *cobra.Command { c, _ := dbSetCmd(g); return c }

// dbSetCmd also returns the flags it fills, so tests can parse without running.
func dbSetCmd(g *globals) (*cobra.Command, *dbFlags) {
	f := &dbFlags{}
	c := &cobra.Command{
		Use:   "set",
		Short: "Change the database connection or the databases to back up; tested before it is saved",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			s, err := f.settings(c.Flags(), c.InOrStdin(), promptPassword(c.ErrOrStderr()))
			if err != nil {
				return err
			}
			d, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Detection, error) {
				return cl.SetDatabase(ctx, s)
			})
			if err != nil {
				return err
			}
			fmt.Fprintln(c.OutOrStdout(), "saved")
			renderDetection(c.OutOrStdout(), d)
			return nil
		},
	}
	fl := c.Flags()
	fl.StringVar(&f.host, "host", "", "database host")
	fl.IntVar(&f.port, "port", 0, "database port")
	fl.StringVar(&f.user, "user", "", "database user")
	fl.StringVar(&f.dumper, "dumper", "", "path to mysqldump (default: XAMPP, then PATH)")
	fl.StringSliceVar(&f.dbs, "db", nil, "databases to back up, comma separated")
	fl.BoolVar(&f.password, "password", false, "prompt for the password")
	fl.BoolVar(&f.passwordStdin, "password-stdin", false, "read the password from the first line of stdin")
	fl.BoolVar(&f.noPassword, "no-password", false, "connect without a password")
	return c, f
}
```

```go
// settings turns the flags given into a partial update; flags left out keep
// the agent's current value.
func (f *dbFlags) settings(fs *pflag.FlagSet, stdin io.Reader, prompt func() (string, error)) (localapi.DBSettings, error) {
	var s localapi.DBSettings
	n := 0
	for _, b := range []bool{f.password, f.passwordStdin, f.noPassword} {
		if b {
			n++
		}
	}
	if n > 1 {
		return s, usagef("use only one of --password, --password-stdin and --no-password")
	}
	if fs.Changed("host") {
		s.Host = &f.host
	}
	if fs.Changed("port") {
		s.Port = &f.port
	}
	if fs.Changed("user") {
		s.User = &f.user
	}
	if fs.Changed("dumper") {
		s.Dumper = &f.dumper
	}
	if fs.Changed("db") {
		s.Databases = append([]string{}, f.dbs...)
	}
	switch {
	case f.noPassword:
		empty := ""
		s.Password = &empty
	case f.passwordStdin:
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return s, fail(err)
		}
		pw := strings.TrimRight(line, "\r\n")
		if pw == "" {
			return s, usagef("--password-stdin read an empty line; use --no-password for no password")
		}
		s.Password = &pw
	case f.password:
		pw, err := prompt()
		if err != nil {
			return s, fail(err)
		}
		s.Password = &pw
	}
	if s.Host == nil && s.Port == nil && s.User == nil && s.Dumper == nil && s.Databases == nil && s.Password == nil {
		return s, usagef("nothing to change; see 'sevjala db set --help'")
	}
	return s, nil
}

func promptPassword(w io.Writer) func() (string, error) {
	return func() (string, error) {
		fmt.Fprint(w, "database password: ")
		b, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(w)
		if err != nil {
			return "", fmt.Errorf("read the password (no terminal? use --password-stdin): %w", err)
		}
		return string(b), nil
	}
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func renderDetection(w io.Writer, d localapi.Detection) {
	fmt.Fprintf(w, "dumper  %s\nclient  %s\nserver  %s %s\n\n", d.Dumper, d.Client, d.Flavour, d.ServerVersion)
	if len(d.Databases) == 0 {
		fmt.Fprintln(w, "no databases besides the system ones")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "DATABASE\tTABLES\tMODE\tCONFIGURED")
	configured := false
	for _, db := range d.Databases {
		mode := "single-transaction"
		if len(db.NonInnoDB) > 0 {
			mode = "lock-tables (not InnoDB: " + strings.Join(db.NonInnoDB, ", ") + ")"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\n", db.Name, db.Tables, mode, yesNo(db.Configured))
		configured = configured || db.Configured
	}
	tw.Flush()
	if !configured {
		fmt.Fprintln(w, "\nchoose what to back up: sevjala db set --db <name>[,<name>]")
	}
}
```

- [ ] **Step 4: Implement `cmd/backup.go`**

```go
package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/localapi"
)

func newBackupCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "backup", Short: "Run a backup (controller)"}
	var db string
	var tags []string
	now := &cobra.Command{
		Use:   "now",
		Short: "Dump the database now and push it to every paired node",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			jobs, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.BackupJob, error) {
				return cl.BackupNow(ctx, db, tags)
			})
			if err != nil {
				return err
			}
			dataDir, err := g.dataDir()
			if err != nil {
				return fail(err)
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
			defer stop()
			ok, err := watchBackups(ctx, localapi.NewClient(localapi.Endpoint(dataDir)).Backup, jobs, c.OutOrStdout(), time.Second)
			switch {
			case ctx.Err() != nil:
				fmt.Fprintln(c.ErrOrStderr(), "still running in the agent; see 'sevjala backups list'")
				return &exitError{code: 130}
			case err != nil:
				return failf("agent stopped answering: %w", err)
			case !ok:
				return errSilent1
			}
			return nil
		},
	}
	now.Flags().StringVar(&db, "db", "", "back up only this configured database")
	now.Flags().StringSliceVar(&tags, "tag", nil, "tag the backup (default: manual)")
	c.AddCommand(now)
	return c
}

// watchBackups polls each backup until it settles and prints every change.
// ok is false when a dump failed or a copy did not verify.
func watchBackups(ctx context.Context, get func(context.Context, string) (localapi.Backup, error),
	jobs []localapi.BackupJob, w io.Writer, every time.Duration) (bool, error) {
	ok := true
	for _, j := range jobs {
		jok, err := watchBackup(ctx, get, j.BackupID, w, every)
		if err != nil || ctx.Err() != nil {
			return false, err
		}
		ok = ok && jok
	}
	return ok, nil
}

func watchBackup(ctx context.Context, get func(context.Context, string) (localapi.Backup, error), id string, w io.Writer, every time.Duration) (bool, error) {
	var last string
	var printed time.Time
	for {
		b, err := get(ctx, id)
		if err != nil {
			if ctx.Err() != nil {
				return false, nil
			}
			return false, err
		}
		line, done, good := backupLines(b)
		if line != last && !(b.State == localapi.BackupDumping && last != "" && time.Since(printed) < 5*time.Second) {
			fmt.Fprintln(w, line)
			last, printed = line, time.Now()
		}
		if done {
			return good, nil
		}
		select {
		case <-ctx.Done():
			return false, nil
		case <-time.After(every):
		}
	}
}

// backupLines renders one poll; done once nothing changes without an M3
// retry, good when every copy is verified.
func backupLines(b localapi.Backup) (lines string, done, good bool) {
	head := b.BackupID + " " + b.Database + ":"
	switch b.State {
	case localapi.BackupDumping:
		return fmt.Sprintf("%s dumping… %s", head, humanBytes(b.Bytes)), false, false
	case localapi.BackupFailed:
		return fmt.Sprintf("%s dump failed: %s", head, b.Error), true, false
	}
	lines = fmt.Sprintf("%s %s %s sha256:%s", head, b.State, humanBytes(b.Size), b.SHA256[:min(12, len(b.SHA256))])
	if len(b.Copies) == 0 {
		return lines + "\n  no paired nodes: the backup is only in staging on this PC", true, true
	}
	parts := make([]string, len(b.Copies))
	good = true
	for i, c := range b.Copies {
		parts[i] = c.Hostname + " " + c.State
		if c.State != "verified" {
			good = false
			if c.LastError != "" {
				parts[i] += " (" + c.LastError + ")"
			}
		}
	}
	return lines + "\n  " + strings.Join(parts, " · "), b.State == "distributed" || b.State == "partial", good
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

func newBackupsCmd(g *globals) *cobra.Command {
	c := &cobra.Command{Use: "backups", Short: "Backups the controller knows about"}
	var matrix bool
	list := &cobra.Command{
		Use:   "list",
		Short: "List backups and where their copies are",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			bs, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.Backup, error) {
				return cl.Backups(ctx)
			})
			if err != nil {
				return err
			}
			if matrix {
				renderMatrix(c.OutOrStdout(), bs, runtime.GOOS)
			} else {
				renderBackups(c.OutOrStdout(), bs)
			}
			return nil
		},
	}
	list.Flags().BoolVar(&matrix, "matrix", false, "one column per node")
	c.AddCommand(list)
	return c
}

func renderBackups(w io.Writer, bs []localapi.Backup) {
	if len(bs) == 0 {
		fmt.Fprintln(w, "no backups yet; run 'sevjala backup now'")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BACKUP\tDATABASE\tCREATED\tSIZE\tTAGS\tSTATE\tCOPIES")
	for _, b := range bs {
		created := "-"
		if !b.CreatedAt.IsZero() {
			created = b.CreatedAt.Local().Format("2006-01-02 15:04")
		}
		verified := 0
		for _, c := range b.Copies {
			if c.State == "verified" {
				verified++
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d/%d verified\n", b.BackupID, b.Database, created, humanBytes(b.Size),
			strings.Join(b.Tags, ","), b.State, verified, len(b.Copies))
	}
	tw.Flush()
}

// renderMatrix uses ASCII marks on Windows, whose console code pages lack ✓.
func renderMatrix(w io.Writer, bs []localapi.Backup, goos string) {
	marks := map[string]string{"verified": "✓", "pending": "…", "transferring": "…", "failed": "✗", "offline": "○"}
	if goos == "windows" {
		marks = map[string]string{"verified": "v", "pending": ".", "transferring": ".", "failed": "x", "offline": "o"}
	}
	var hosts []string
	for _, b := range bs {
		for _, c := range b.Copies {
			if !slices.Contains(hosts, c.Hostname) {
				hosts = append(hosts, c.Hostname)
			}
		}
	}
	slices.Sort(hosts)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "BACKUP\tDATABASE\t"+strings.Join(hosts, "\t"))
	for _, b := range bs {
		cells := make([]string, len(hosts))
		for i, h := range hosts {
			cells[i] = "-"
			for _, c := range b.Copies {
				if c.Hostname == h {
					cells[i] = marks[c.State]
				}
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", b.BackupID, b.Database, strings.Join(cells, "\t"))
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%s verified  %s pending  %s failed  %s offline  - no copy planned\n",
		marks["verified"], marks["pending"], marks["failed"], marks["offline"])
}
```

In `cmd/root.go`, append `newDBCmd(g)`, `newBackupCmd(g)` and `newBackupsCmd(g)` to `root.AddCommand`.

- [ ] **Step 5: Implement the doctor changes**

In `cmd/doctor.go`:
- `checkResult` gains a `Warn bool` field, set when the check is not OK but must not fail doctor.
- Replace `checkDataDir` with `checkWritable(name, dir string)`, which has the same body and uses `name`.

`renderChecks`:

```go
func renderChecks(w io.Writer, rs []checkResult) bool {
	all := true
	for _, r := range rs {
		mark := "ok"
		switch {
		case r.OK:
		case r.Warn:
			mark = "WARN"
		default:
			mark, all = "FAIL", false
		}
		fmt.Fprintf(w, "%-6s%-12s%s\n", mark, r.Name, r.Detail)
	}
	return all
}

// databaseChecks reports what the agent sees through 'db detect'.
func databaseChecks(d localapi.Detection, err error) []checkResult {
	if err != nil {
		return []checkResult{{Name: "database", Detail: err.Error()}}
	}
	server := d.Flavour + " " + d.ServerVersion
	var parts []string
	for _, db := range d.Databases {
		if db.Configured {
			parts = append(parts, db.Name+" "+strings.TrimPrefix(db.Consistency, "--"))
		}
	}
	dbCheck := checkResult{Name: "database", Detail: server + " · " + strings.Join(parts, ", "), OK: true}
	if len(parts) == 0 {
		dbCheck = checkResult{Name: "database", Detail: server + "; nothing to back up yet: sevjala db set --db <name>", Warn: true}
	}
	return []checkResult{{Name: "dumper", Detail: d.Dumper, OK: true}, dbCheck}
}

func storeCheck(s *localapi.StoreInfo, minFree int64) checkResult {
	if s == nil {
		return checkResult{Name: "store", Detail: "the agent reported no store"}
	}
	detail := fmt.Sprintf("%s · %s free · %d backups", s.Path, humanBytes(s.FreeBytes), s.Backups)
	switch {
	case s.FreeBytes < minFree:
		return checkResult{Name: "store", Detail: detail + " · under the " + humanBytes(minFree) + " floor"}
	case len(s.Problems) > 0:
		return checkResult{Name: "store", Detail: detail + " · " + strings.Join(s.Problems, "; "), Warn: true}
	}
	return checkResult{Name: "store", Detail: detail, OK: true}
}
```

In `newDoctorCmd`, `rs` starts with `checkWritable("data dir", dataDir)`. After `rs = append(rs, checkLAN(*cfg, id))`, add:

```go
				if apiErr == nil {
					switch cfg.Role {
					case config.RoleController:
						rs = append(rs, checkWritable("staging", config.StagingDir(dataDir)))
						ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
						d, err := localapi.NewClient(endpoint).DetectDatabase(ctx)
						cancel()
						rs = append(rs, databaseChecks(d, err)...)
					case config.RoleNode:
						rs = append(rs, storeCheck(st.Store, cfg.Node.Store.MinFreeBytes))
					}
				}
```

Add `strings` to the imports. Update the doctor `Short` to `"Check the data dir, config, identity, service, APIs, database or store"`.

- [ ] **Step 6: Run the tests**

Run: `go test -race ./cmd/ && go build ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum cmd
git commit -m "feat(cmd): db detect/set, backup now with progress, backups list --matrix, doctor database and store checks"
```

---

### Task 13: Real-database integration test (`make test-integration`)

**Files:**
- Create: `dump/integration_test.go`, `scripts/test-integration.sh`
- Modify: `Makefile`

**Interfaces:**
- Consumes: `dump.FindTools`, `Detect`, `Run`.

- [ ] **Step 1: Write the integration test**

`dump/integration_test.go`:

```go
//go:build integration

package dump

import (
	"bytes"
	"compress/gzip"
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/inventory"
)

// Run by 'make test-integration' inside a mariadb:10.4 container.
func TestIntegrationDumpRestoreChecksum(t *testing.T) {
	addr := os.Getenv("SEVJALA_TEST_DB")
	if addr == "" {
		t.Skip("SEVJALA_TEST_DB not set; run 'make test-integration'")
	}
	host, portS, _ := net.SplitHostPort(addr)
	port, _ := strconv.Atoi(portS)
	conn := Conn{Host: host, Port: port, User: "root"}
	tools, err := FindTools("")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sql := func(stdin []byte, q ...string) string {
		t.Helper()
		args := append(conn.args(), "--batch", "--skip-column-names")
		if len(q) == 1 {
			args = append(args, "-e", q[0])
		} else {
			args = append(args, q...)
		}
		cmd := exec.CommandContext(ctx, tools.Client, args...)
		cmd.Env = conn.env()
		cmd.Stdin = bytes.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", q, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	sql(nil, `DROP DATABASE IF EXISTS sj_shop; DROP DATABASE IF EXISTS sj_legacy;
		CREATE DATABASE sj_shop CHARACTER SET utf8mb4; CREATE DATABASE sj_legacy;
		CREATE TABLE sj_shop.orders (id INT PRIMARY KEY, note VARCHAR(64)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
		INSERT INTO sj_shop.orders WITH RECURSIVE s(n) AS (SELECT 1 UNION ALL SELECT n + 1 FROM s WHERE n < 5000)
			SELECT n, CONCAT('order ', n) FROM s;
		INSERT INTO sj_shop.orders VALUES (0, 'party 🎉');
		CREATE PROCEDURE sj_shop.hello() SELECT 1;
		CREATE TABLE sj_legacy.posts (id INT PRIMARY KEY, body TEXT) ENGINE=MyISAM;
		INSERT INTO sj_legacy.posts VALUES (1, 'a'), (2, 'b');`)

	d, err := Detect(ctx, tools, conn)
	if err != nil {
		t.Fatal(err)
	}
	modes := map[string]string{}
	for _, db := range d.Databases {
		modes[db.Name] = db.Consistency()
	}
	if modes["sj_shop"] != "--single-transaction" || modes["sj_legacy"] != "--lock-tables" || d.Flavour != "MariaDB" {
		t.Fatalf("detection %+v", d)
	}

	staging := t.TempDir()
	for _, db := range []struct{ name, table string }{{"sj_shop", "orders"}, {"sj_legacy", "posts"}} {
		m, err := Run(ctx, Options{ID: inventory.NewID(time.Now()), Database: db.name, StagingDir: staging,
			SourceAgent: "it", Created: time.Now(), Tools: tools, Conn: conn})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(staging, m.BackupID+inventory.DataExt))
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		var plain bytes.Buffer
		if _, err := plain.ReadFrom(zr); err != nil {
			t.Fatal(err)
		}
		restored := db.name + "_restored"
		sql(nil, "DROP DATABASE IF EXISTS "+restored+"; CREATE DATABASE "+restored+" CHARACTER SET utf8mb4")
		sql(plain.Bytes(), restored)
		sums := strings.Split(sql(nil, "CHECKSUM TABLE "+db.name+"."+db.table+", "+restored+"."+db.table), "\n")
		if len(sums) != 2 || strings.Fields(sums[0])[1] != strings.Fields(sums[1])[1] {
			t.Errorf("%s checksums differ: %q", db.name, sums)
		}
	}
	if got := sql(nil, "SELECT note FROM sj_shop_restored.orders WHERE id = 0"); got != "party 🎉" {
		t.Errorf("emoji row = %q", got)
	}
	if got := sql(nil, "SELECT COUNT(*) FROM information_schema.ROUTINES WHERE ROUTINE_SCHEMA = 'sj_shop_restored'"); got != "1" {
		t.Errorf("routines restored = %s", got)
	}
}
```

- [ ] **Step 2: Write the script and the Makefile target**

`scripts/test-integration.sh`:

```sh
#!/bin/sh
# Real-database tests: MariaDB 10.4 (XAMPP's line) in Docker; the test binary
# runs inside the container, which already has mysqldump and mysql.
set -eu
image=mariadb:10.4
name="sevjala-it-$$"
out=$(mktemp -d)
trap 'docker rm -f "$name" >/dev/null 2>&1 || true; rm -rf "$out"' EXIT

docker run -d --name "$name" -e MARIADB_ALLOW_EMPTY_ROOT_PASSWORD=1 "$image" >/dev/null
arch=$(docker version -f '{{.Server.Arch}}')
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go test -c -tags integration -o "$out/dump.test" ./dump

i=0
until docker exec "$name" mysqladmin --protocol=TCP -h127.0.0.1 ping >/dev/null 2>&1; do
	i=$((i + 1))
	[ "$i" -lt 60 ] || { docker logs "$name"; exit 1; }
	sleep 1
done
docker cp "$out/dump.test" "$name:/tmp/dump.test"
docker exec -e SEVJALA_TEST_DB=127.0.0.1:3306 "$name" /tmp/dump.test -test.v -test.run Integration
```

`Makefile`: add `test-integration` to `.PHONY` and:

```make
test-integration:
	sh scripts/test-integration.sh
```

- [ ] **Step 3: Run it**

Run: `chmod +x scripts/test-integration.sh && make test-integration`
Expected: `--- PASS: TestIntegrationDumpRestoreChecksum`. The first run pulls `mariadb:10.4`.

Also run `go vet -tags integration ./dump/`; expect no output.

- [ ] **Step 4: Commit**

```bash
git add dump/integration_test.go scripts/test-integration.sh Makefile
git commit -m "test(dump): real MariaDB 10.4 dump, restore and CHECKSUM TABLE via make test-integration"
```

---

### Task 14: Docs, full verification, local end-to-end run

**Files:**
- Modify: `docs/protocol.md`, `docs/decisions.md`, `docs/smoke-tests.md`, `docs/PROGRESS.md`, `README.md`

- [ ] **Step 1: `docs/protocol.md`**

- Status table rows:
  - LAN API → "`/identity`, `/pair`, `/status`, `PUT /backups/{id}` below; `GET`/`DELETE /backups/{id}` from M3/M4".
  - Manifest → "implemented below (M2)".
  - Node store directory → "implemented below (M2)".
  - `GET /status` → "implemented below (M2)".
- Add sections:
  - **Manifest (since M2):** the JSON example from the blueprint plus `--default-character-set=utf8mb4` in `dump_flags`; the field table; ID regex `^BK-\d{8}-\d{6}-[0-9A-F]{4}$`.
  - **Node store (since M2):**
    - Files: `<id>.sql.gz` and `<id>.json`, `<id>.part` while receiving.
    - Write order: `.part`, then sync, rename, then the manifest via temp, sync, rename, dir sync.
    - The scan states table.
    - `.part` handling: removed at agent start, and at scan time when older than 1 h.
    - Default path `<data-dir>/store`.
  - **Controller staging (since M2):** `<data-dir>/staging/` with the same names; `.part` files removed at agent start.
  - **`PUT /backups/{id}`:** headers, `Expect: 100-continue`, the status codes table (200, 400, 409, 422, 507, 500), 60 s read idle.
- LAN `/status`: replace the M1-subset example with the full one (`store`, `backups[]` with `state` `verified`|`corrupt`, `problems`, `health` `warning` when problems).
- Local API table:
  - `/code` now returns `fp`; `/pair` body gains `fp`.
  - New rows: `POST /db/detect`, `PUT /db`, `POST /backup/now`, `GET /backups`, `GET /backups/{id}`, with their shapes from Task 8.
  - Local `/status` gains `store` (node).
- Controller store: schema version 2 SQL from Task 5; the backup state rule; `last_error` kept until verified.

- [ ] **Step 2: `docs/decisions.md`**

Append rows (date 2026-10-13):

| ID | Decision | Why |
|---|---|---|
| D-018 | `sevjala code` shows the node's first 16 fingerprint hex chars; `pair` requires `--fp` and checks it against the discovered fingerprint before any network call. | Closes I-5: a LAN device answering discovery could capture the HMAC proof and brute-force the 6-digit code offline. 64 bits is out of reach for certificate grinding. |
| D-019 | Detection and (M4) restore use the `mysql`/`mariadb` client binary next to the dumper; no Go MySQL driver. | Keeps the blueprint's dependency list; the client ships with every XAMPP. |
| D-020 | The DB password lives in the OS keyring as service `sevjala`, account `db`, written by the agent (SYSTEM/root) through `db set`. Linux without a Secret Service uses AES-256-GCM `secrets/db.enc` with `secrets/key` (0600) and logs `keyring_fallback`. Children get it only via `MYSQL_PWD`; an inherited `MYSQL_PWD` is dropped. | Blueprint: secrets in the keyring. The CLI user's keyring is not the service's. `MYSQL_PWD` keeps it off argv and disk. |
| D-021 | Dumps add `--default-character-set=utf8mb4` to the blueprint's flags. | Older client defaults (utf8mb3) mangle 4-byte characters. |
| D-022 | New dependencies: `github.com/zalando/go-keyring` (blueprint-named) and `golang.org/x/term` (no-echo password prompt). | `x/term` is the standard no-echo prompt and depends only on `x/sys`. |
| D-023 | Node store default `<data-dir>/store` (inherits the locked ACL); `min_free_bytes` default 2 GiB; a PUT that would cross it is refused upfront with 507. | Backups stay admin-only by default; a full disk fails fast instead of mid-write. |
| D-024 | Until M3 policies, every backup goes to every paired node; `backup now` without `--tag` tags it `manual`. | Owner choice; a manual backup must not look like a scheduled tier. |
| D-025 | A dump in progress or failed exists only in controller memory (`dumping`/`failed`); the `backups` row is written after the rename (blueprint step 4). | No half-made rows; a restart forgets only what the log already recorded. |
| D-026 | Transfer retries after 30 s, 2 min, 10 min; then `failed` (or `offline` when no attempt connected) until M3. 400/403/409 and a TLS rejection are not retried. A restart requeues `pending` and `transferring` copies. | Blueprint backoff without M3's status cycle; permanent errors should not burn twelve minutes. |
| D-027 | `PUT /backups/{id}` uses `Expect: 100-continue`; an identical stored copy answers 200 without the body. Both sides cancel after 60 s without progress. | A lost 200 then costs no re-transfer; a stalled peer frees its slot. |
| D-028 | `backup_copies.last_error` survives retries until the copy is verified; the backup state is recomputed on every copy change. | The operator sees why a copy is retrying. |
| D-029 | Node scan reports every corrupt case (size mismatch, manifest without file, file without manifest, unreadable manifest) as a problem; `health` is `warning` when there are problems; removed `.part` files are reported once by the next scan. | Transparent status without re-hashing on every poll. |
| D-030 | `doctor` gains a `WARN` level (shown, does not fail the run), used for "no database chosen yet" and store problems. | A fresh controller is not broken, only unfinished. |

Also, in "Open questions", tick the XAMPP-version line only if the integration run settles it. It does not; leave it open.

- [ ] **Step 3: `docs/smoke-tests.md`: add the M2 section**

Steps (the owner runs them on real machines):

1. **Controller (XAMPP PC):**
   - `sevjala db detect` lists `mysqldump.exe` and the databases.
   - `sevjala db set --db <name>`; add `--password` if root has one.
   - `doctor` shows `ok dumper`, `ok database … single-transaction`.
2. **Three node PCs:**
   - Pair each with `sevjala code` → `sevjala pair <host> --code N --fp F`.
   - Pair one PC with a deliberately wrong `--fp` and see "do not pair".
3. **300 MB database:**
   - `sevjala backup now` shows dumping → created → three `verified`; the exit code is 0.
   - `sevjala backups list --matrix` shows three ✓ (v on Windows).
4. **On each node:**
   - The `<data-dir>\store` has `BK-….sql.gz` + `.json`.
   - `certutil -hashfile BK-….sql.gz SHA256` (or `shasum -a 256`) equals the controller's `sha256`.
5. **Killed mid-transfer:**
   - Start `backup now`; during `transferring`, run `sevjala service stop` on one node.
   - The controller shows that copy `pending (…)` then `offline`.
   - On the node, `service start`; no `.part` remains in `store` after its start.
6. **Controller restart mid-transfer:**
   - `service stop` / `start` on the controller during `transferring`; the copy finishes `verified` after the start.
7. **Win7 (VM):**
   - `db set --password` stores the password: `cmdkey /list` run as SYSTEM via `psexec -s` shows `sevjala:db`. `config.toml` has `password_ref = "keyring:db"` and no password.
8. **macOS sudo install:**
   - `sudo sevjala db set --password` succeeds (keychain as root); `backup now` works after a reboot.
9. **Log check:**
   - Log events present: `backup_started`, `backup_created`, `transfer_verified`, `backup_received`.
   - `grep -i <password> sevjala.log` finds nothing.

- [ ] **Step 4: `docs/PROGRESS.md` and `README.md`**

PROGRESS M2 row:
- Status: `code done — 3-PC smoke pending ([how to test](./smoke-tests.md))`.
- Links: `[spec](./superpowers/specs/2026-10-13-m2-first-backup-design.md) · [plan](./superpowers/plans/2026-10-13-m2-first-backup.md)`.

README: in "Pair a node", add `--fp`. Add a "First backup" snippet: `sevjala db detect`, `sevjala db set --db shop`, `sevjala backup now`, `sevjala backups list --matrix`.

- [ ] **Step 5: Full verification**

Run, in order, reading each output:

```bash
export PATH="$HOME/.asdf/shims:$PATH"
go vet ./... && GOOS=windows go vet ./... && GOOS=linux go vet ./... && GOOS=windows GOARCH=386 go vet ./...
golangci-lint run
go test -race ./...
go mod tidy -diff
gofmt -l $(git ls-files '*.go')
make test-integration
make build-all
```

Expected:
- vet, gofmt and `go mod tidy -diff` print nothing.
- lint reports `0 issues`.
- Every test package passes, and the integration test passes.
- `build-all` produces six assets, including both win7 ones.

Also run the root-only lockdown tests in Docker as in M1: `docker run --rm -v "$PWD":/src -w /src golang:1.27.2 go test ./svc/ ./inventory/ ./config/`. The Linux keyring fallback test runs there too.

- [ ] **Step 6: Local end-to-end run (evidence for the report)**

1. **Start MariaDB:**
   - `docker run -d --name sj-e2e -e MARIADB_ALLOW_EMPTY_ROOT_PASSWORD=1 -p 13306:3306 mariadb:10.4`.
   - Seed a ~300 MB `e2e` database: one InnoDB table, rows of 1 KiB random hex via a recursive CTE in batches.
2. **Start three node agents** on this Mac with separate `--data-dir`s under `/tmp/sj-n{1,2,3}`. Each `config.toml` has `role = "node"`, `listen = "127.0.0.1:1789{2,3,4}"` and `discovery_port = 1790{2,3,4}`. Run `sevjala agent --data-dir …` in the background.
3. **Start a controller agent inside a `golang:1.27.2` container** on the same Docker network as `sj-e2e`, with the linux binary and `apt-get install -y mariadb-client`.
   - `listen` is a free port; it reaches the nodes via `host.docker.internal:1789x`.
   - Then `agents add host.docker.internal:1789x`, `code` on each node, and `pair` with `--fp`.
4. **Configure and back up:** `db set --host sj-e2e --port 3306 --db e2e`, then `backup now`.
5. **Record:**
   - The transcript, and each node's `store` listing.
   - `shasum -a 256` of one node copy against the manifest.
   - Kill one node agent mid-transfer and check no `.part` remains after its restart.
6. **Clean up the containers and `/tmp/sj-*`.**

If Docker networking makes the controller unreachable to the nodes, record a ruling and fall back to the loopback integration test as the evidence. Say so in the final report; never claim the 3-PC exit criterion.

- [ ] **Step 7: Commit**

```bash
git add docs README.md
git commit -m "docs: M2 protocol formats, decisions D-018..D-030, 3-PC smoke steps"
```
