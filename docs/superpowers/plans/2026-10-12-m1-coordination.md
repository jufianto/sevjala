# M1 Coordination Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Agents get an identity, a Controller discovers Nodes by UDP broadcast, pairs with them by a 6-digit code, and pings them over mutual TLS; unpaired peers fail at the TLS handshake.

**Architecture:** Five new leaf packages (`identity`, `pairing`, `discovery`, `lanapi`, `store`) and one role package (`coord`) that implements a new `localapi.Backend` interface. `svc.Agent` wires them by role; `cmd` adds `discover`, `code`, `pair`, `agents`, `ping`. Install locks the data dir down and opens the Windows firewall; doctor checks both.

**Tech Stack:** Go 1.27.2 stdlib (`crypto/tls`, `crypto/ecdsa`, `net`, `net/http`), `modernc.org/sqlite` v1.60.1 (new), `golang.org/x/sys/windows` (ACLs), existing cobra / kardianos/service / go-winio / BurntSushi/toml.

**Spec:** `docs/superpowers/specs/2026-10-12-m1-coordination-design.md`

## Global Constraints

- Pure Go, `CGO_ENABLED=0`; everything compiles with the go-win7 SDK (`make build-win7`).
- Packages at the repo root, no `internal/` (D-001).
- New dependency: `modernc.org/sqlite` only (blueprint-named). Nothing else.
- Secrets (pairing code, proof, private key) never appear in logs, `/status`, test fixtures or commits.
- Ports: discovery `17890/udp`, LAN API `17891/tcp`, both from `config.toml`.
- TLS 1.2 minimum; fingerprint format `sha256:<64 lowercase hex>` of the DER cert.
- Pairing code: 6 digits, 10-minute TTL, 5 wrong attempts burn it, memory only.
- Errors wrap with `%w`; logs are `log/slog` with an `event` attribute.
- Exit codes: 0 ok, 1 operational error, 2 usage error.
- Tests table-driven where there is a table; anything on the LAN or local API gets a loopback test.
- Verify order: `go vet ./...` (also `GOOS=darwin`, `GOOS=windows`) → `golangci-lint run` → `go test -race ./...`.
- Shell PATH: prefix commands with `export PATH="$HOME/.asdf/shims:$PATH";` so Go 1.27.2 and golangci-lint v2 are used.

## Review Focus

1. A paired node gets a new IP from DHCP → `sevjala discover` must refresh the stored address so `ping` works again (Task 7 test `TestDiscoverRefreshesPairedAddress`).
2. A node agent restarts after pairing → it must still accept its controller and reject others (Task 4 test `TestPairingSurvivesRestart`).
3. The operator pairs a controller by mistake → a clear "only nodes can be paired" error, not a TLS failure (Task 7 test `TestPairRefusesController`).
4. Two PCs share a hostname → `ping <hostname>` must list both candidates, not pick one (Task 7 test `TestResolve`, "ambiguous hostname" row).
5. A controller with no broadcast-capable interface (cable unplugged) → `discover` explains that, not "0 agents" (Task 3 test `TestProbeWithoutTargets`).

---

### Task 1: identity, events, config port helper

**Files:**
- Create: `identity/identity.go`, `identity/peers.go`, `identity/identity_test.go`
- Modify: `logx/logx.go` (event constants), `config/config.go` (`DefaultLANPort`, `ListenPort`), `config/config_test.go`

**Interfaces:**
- Produces:
  - `identity.Identity{AgentID string; Cert tls.Certificate; FP string}` (`Cert.Leaf` set)
  - `identity.LoadOrCreate(dataDir string) (*Identity, bool /*created*/, error)`
  - `identity.Load(dataDir string) (*Identity, error)` — wraps `fs.ErrNotExist` only when `agent_id` is missing
  - `identity.Fingerprint(der []byte) string`
  - `identity.Dir(dataDir string) string`
  - `identity.Peer{AgentID, FP string; PairedAt time.Time}`; `identity.LoadController(dataDir) (*Peer, error)` (nil, nil when unpaired); `identity.SaveController(dataDir string, p Peer) error`
  - `logx.EventIdentityCreated`, `EventDiscoveryRun`, `EventAgentAdded`, `EventPairCodeIssued`, `EventPairSucceeded`, `EventPairRejected`, `EventPeerRejected`
  - `config.DefaultLANPort = 17891`; `(config.Config).ListenPort() int`

- [ ] **Step 1: Write the failing tests**

`identity/identity_test.go`:

```go
package identity

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

var (
	uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	fpRe   = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func TestCreateThenLoad(t *testing.T) {
	dir := t.TempDir()
	id, created, err := LoadOrCreate(dir)
	if err != nil || !created {
		t.Fatalf("first LoadOrCreate: created=%v err=%v", created, err)
	}
	if !uuidRe.MatchString(id.AgentID) {
		t.Errorf("agent_id %q is not a UUIDv4", id.AgentID)
	}
	if !fpRe.MatchString(id.FP) {
		t.Errorf("fp %q", id.FP)
	}
	if cn := id.Cert.Leaf.Subject.CommonName; cn != id.AgentID {
		t.Errorf("CN %q != agent_id %q", cn, id.AgentID)
	}
	if id.Cert.Leaf.NotAfter.Before(time.Now().Add(9 * 365 * 24 * time.Hour)) {
		t.Errorf("cert expires %v, want ~10 years", id.Cert.Leaf.NotAfter)
	}
	again, created, err := LoadOrCreate(dir)
	if err != nil || created {
		t.Fatalf("second LoadOrCreate: created=%v err=%v", created, err)
	}
	if again.AgentID != id.AgentID || again.FP != id.FP {
		t.Errorf("reload changed identity: %s %s -> %s %s", id.AgentID, id.FP, again.AgentID, again.FP)
	}
}

func TestKeyIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows relies on the data dir ACL")
	}
	dir := t.TempDir()
	if _, _, err := LoadOrCreate(dir); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]os.FileMode{"key.pem": 0o600, "": 0o700} {
		fi, err := os.Stat(filepath.Join(Dir(dir), name))
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%q mode %v, want %v", name, got, want)
		}
	}
}

func TestMissingAgentIDRegenerates(t *testing.T) {
	dir := t.TempDir()
	first, _, _ := LoadOrCreate(dir)
	os.Remove(filepath.Join(Dir(dir), "agent_id"))
	second, created, err := LoadOrCreate(dir)
	if err != nil || !created || second.AgentID == first.AgentID {
		t.Fatalf("created=%v err=%v same id=%v", created, err, second != nil && second.AgentID == first.AgentID)
	}
}

func TestBrokenIdentityIsAnError(t *testing.T) {
	tests := []struct {
		name  string
		break_ func(dir string)
		want  string
	}{
		{"missing cert", func(dir string) { os.Remove(filepath.Join(Dir(dir), "cert.pem")) }, "unusable"},
		{"other agent_id", func(dir string) {
			os.WriteFile(filepath.Join(Dir(dir), "agent_id"), []byte("00000000-0000-4000-8000-000000000000\n"), 0o644)
		}, "does not match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			LoadOrCreate(dir)
			tt.break_(dir)
			_, _, err := LoadOrCreate(dir)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.want)
			}
			if errors.Is(err, fs.ErrNotExist) {
				t.Error("a broken identity must not look like a fresh install")
			}
		})
	}
}

func TestControllerPeerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if p, err := LoadController(dir); p != nil || err != nil {
		t.Fatalf("unpaired: %+v %v", p, err)
	}
	want := Peer{AgentID: "c1", FP: "sha256:ab", PairedAt: time.Now().UTC().Truncate(time.Second)}
	if err := SaveController(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadController(dir)
	if err != nil || got == nil || got.AgentID != want.AgentID || got.FP != want.FP || !got.PairedAt.Equal(want.PairedAt) {
		t.Fatalf("got %+v %v, want %+v", got, err, want)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(Dir(dir), "peers.json"))
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("peers.json mode %v", fi.Mode().Perm())
		}
	}
}
```

Append to `config/config_test.go`:

```go
func TestListenPort(t *testing.T) {
	if got := Default(RoleNode).ListenPort(); got != DefaultLANPort {
		t.Errorf("ListenPort = %d, want %d", got, DefaultLANPort)
	}
	c := Default(RoleNode)
	c.Listen = "192.168.1.5:9000"
	if got := c.ListenPort(); got != 9000 {
		t.Errorf("ListenPort = %d, want 9000", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./identity ./config`
Expected: FAIL to compile — `undefined: LoadOrCreate`, `c.ListenPort undefined`.

- [ ] **Step 3: Implement**

`identity/identity.go`:

```go
// Package identity is the agent's persistent self: agent_id and a self-signed
// certificate whose fingerprint peers pin (blueprint: Coordination → Identity).
package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DirName  = "identity"
	idFile   = "agent_id"
	certFile = "cert.pem"
	keyFile  = "key.pem"
	validity = 10 * 365 * 24 * time.Hour
)

type Identity struct {
	AgentID string
	Cert    tls.Certificate
	FP      string
}

func Dir(dataDir string) string { return filepath.Join(dataDir, DirName) }

func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// LoadOrCreate creates a new identity only when agent_id does not exist; any
// other problem is an error so an identity is never silently replaced.
func LoadOrCreate(dataDir string) (*Identity, bool, error) {
	id, err := Load(dataDir)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	if err := create(dataDir); err != nil {
		return nil, false, fmt.Errorf("create identity: %w", err)
	}
	id, err = Load(dataDir)
	return id, err == nil, err
}

// Load wraps fs.ErrNotExist only when agent_id is missing.
func Load(dataDir string) (*Identity, error) {
	dir := Dir(dataDir)
	raw, err := os.ReadFile(filepath.Join(dir, idFile))
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	agentID := strings.TrimSpace(string(raw))
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, certFile), filepath.Join(dir, keyFile))
	if err != nil {
		return nil, fmt.Errorf("identity in %s is unusable: %s", dir, err.Error())
	}
	if cert.Leaf == nil {
		if cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return nil, fmt.Errorf("identity in %s is unusable: %s", dir, err.Error())
		}
	}
	if cn := cert.Leaf.Subject.CommonName; cn != agentID {
		return nil, fmt.Errorf("identity in %s is unusable: certificate CN %q does not match agent_id %q", dir, cn, agentID)
	}
	return &Identity{AgentID: agentID, Cert: cert, FP: Fingerprint(cert.Leaf.Raw)}, nil
}

// create writes agent_id last, so a crash part-way leaves "no identity yet".
func create(dataDir string) error {
	dir := Dir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	agentID, err := newUUID()
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: agentID},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, keyFile), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, certFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, idFile), []byte(agentID+"\n"), 0o644)
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
```

`identity/peers.go`:

```go
package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const peersFile = "peers.json"

// Peer is the controller a node trusts (one per node in v1).
type Peer struct {
	AgentID  string    `json:"agent_id"`
	FP       string    `json:"fp"`
	PairedAt time.Time `json:"paired_at"`
}

type peers struct {
	V          int   `json:"v"`
	Controller *Peer `json:"controller"`
}

// LoadController returns nil, nil when the node has never been paired.
func LoadController(dataDir string) (*Peer, error) {
	path := filepath.Join(Dir(dataDir), peersFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p peers
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if p.V != 1 {
		return nil, fmt.Errorf("%s: unsupported version %d", path, p.V)
	}
	return p.Controller, nil
}

func SaveController(dataDir string, c Peer) error {
	raw, err := json.MarshalIndent(peers{V: 1, Controller: &c}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(dataDir), 0o700); err != nil {
		return err
	}
	return writeFile(filepath.Join(Dir(dataDir), peersFile), append(raw, '\n'), 0o600)
}
```

`config/config.go`: add to the const block `DefaultLANPort = 17891` and, after `Validate`:

```go
// ListenPort is the TCP port of Listen; Validate guarantees it parses.
func (c Config) ListenPort() int {
	_, port, _ := net.SplitHostPort(c.Listen)
	p, _ := strconv.Atoi(port)
	return p
}
```

`logx/logx.go`: extend the event block:

```go
const (
	EventAgentStarted       = "agent_started"
	EventAgentStopped       = "agent_stopped"
	EventServiceInstalled   = "service_installed"
	EventServiceUninstalled = "service_uninstalled"
	EventIdentityCreated    = "identity_created"
	EventDiscoveryRun       = "discovery_run"
	EventAgentAdded         = "agent_added"
	EventPairCodeIssued     = "pair_code_issued"
	EventPairSucceeded      = "pair_succeeded"
	EventPairRejected       = "pair_rejected"
	EventPeerRejected       = "peer_rejected"
)
```

- [ ] **Step 4: Run them to verify they pass**

Run: `go test -race ./identity ./config ./logx`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add identity config logx
git commit -m "feat(identity): agent_id and self-signed P-256 cert, controller peer file"
```

---

### Task 2: pairing codes

**Files:**
- Create: `pairing/pairing.go`, `pairing/pairing_test.go`

**Interfaces:**
- Produces:
  - `pairing.TTL = 10*time.Minute`, `pairing.MaxAttempts = 5`
  - `pairing.ErrNoCode`, `ErrBadProof`, `ErrBurnt` (error messages are shown to operators)
  - `pairing.New(now func() time.Time) *Codes`
  - `(*Codes).Issue() (code string, expires time.Time, err error)`, `Active() bool`, `Check(controllerFP, nodeFP, proof string) error`
  - `pairing.Proof(code, controllerFP, nodeFP string) string`, `pairing.ValidCode(s string) bool`

- [ ] **Step 1: Write the failing tests**

`pairing/pairing_test.go`:

```go
package pairing

import (
	"errors"
	"regexp"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

const cfp, nfp = "sha256:c", "sha256:n"

func issued(t *testing.T) (*Codes, *clock, string) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)}
	c := New(clk.now)
	code, exp, err := c.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{6}$`).MatchString(code) || !exp.Equal(clk.t.Add(TTL)) {
		t.Fatalf("code %q expires %v", code, exp)
	}
	return c, clk, code
}

func wrong(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

func TestCorrectProofClearsCode(t *testing.T) {
	c, _, code := issued(t)
	if err := c.Check(cfp, nfp, Proof(code, cfp, nfp)); err != nil {
		t.Fatal(err)
	}
	if err := c.Check(cfp, nfp, Proof(code, cfp, nfp)); !errors.Is(err, ErrNoCode) {
		t.Errorf("second use: %v, want ErrNoCode", err)
	}
}

func TestProofIsBoundToBothFingerprints(t *testing.T) {
	c, _, code := issued(t)
	if err := c.Check(cfp, nfp, Proof(code, "sha256:other", nfp)); !errors.Is(err, ErrBadProof) {
		t.Errorf("proof for another controller: %v", err)
	}
}

func TestFifthWrongProofBurns(t *testing.T) {
	c, _, code := issued(t)
	for i := 1; i < MaxAttempts; i++ {
		if err := c.Check(cfp, nfp, Proof(wrong(code), cfp, nfp)); !errors.Is(err, ErrBadProof) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := c.Check(cfp, nfp, Proof(wrong(code), cfp, nfp)); !errors.Is(err, ErrBurnt) {
		t.Fatalf("attempt %d: %v, want ErrBurnt", MaxAttempts, err)
	}
	if err := c.Check(cfp, nfp, Proof(code, cfp, nfp)); !errors.Is(err, ErrNoCode) {
		t.Errorf("right code after burn: %v", err)
	}
}

func TestCodeExpires(t *testing.T) {
	c, clk, code := issued(t)
	clk.t = clk.t.Add(TTL)
	if c.Active() {
		t.Error("active at expiry")
	}
	if err := c.Check(cfp, nfp, Proof(code, cfp, nfp)); !errors.Is(err, ErrNoCode) {
		t.Errorf("expired code: %v", err)
	}
}

func TestReissueReplacesAndResetsAttempts(t *testing.T) {
	c, _, first := issued(t)
	for i := 1; i < MaxAttempts; i++ {
		c.Check(cfp, nfp, Proof(wrong(first), cfp, nfp))
	}
	second, _, _ := c.Issue()
	if second != first {
		if err := c.Check(cfp, nfp, Proof(first, cfp, nfp)); !errors.Is(err, ErrBadProof) {
			t.Errorf("old code after reissue: %v", err)
		}
	}
	if err := c.Check(cfp, nfp, Proof(second, cfp, nfp)); err != nil {
		t.Errorf("new code: %v", err)
	}
}

func TestValidCode(t *testing.T) {
	for s, want := range map[string]bool{"123456": true, "012345": true, "12345": false, "1234567": false, "12a456": false, "": false} {
		if ValidCode(s) != want {
			t.Errorf("ValidCode(%q) = %v", s, !want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./pairing`
Expected: FAIL to compile — `undefined: New`.

- [ ] **Step 3: Implement**

`pairing/pairing.go`:

```go
// Package pairing holds a node's one-time pairing code and checks the
// controller's HMAC proof (blueprint: Coordination → Pairing).
package pairing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

const (
	TTL         = 10 * time.Minute
	MaxAttempts = 5
)

var (
	ErrNoCode   = errors.New("no active pairing code; run 'sevjala code' on the node")
	ErrBadProof = errors.New("wrong pairing code")
	ErrBurnt    = errors.New("too many wrong codes; run 'sevjala code' on the node again")
)

// Codes keeps the code in memory only; an agent restart clears it.
type Codes struct {
	mu       sync.Mutex
	now      func() time.Time
	code     string
	expires  time.Time
	attempts int
}

func New(now func() time.Time) *Codes { return &Codes{now: now} }

// Issue replaces any active code.
func (c *Codes) Issue() (string, time.Time, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", time.Time{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.code, c.expires, c.attempts = fmt.Sprintf("%06d", n.Int64()), c.now().Add(TTL), 0
	return c.code, c.expires, nil
}

func (c *Codes) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active()
}

func (c *Codes) active() bool {
	if c.code != "" && !c.now().Before(c.expires) {
		c.code = ""
	}
	return c.code != ""
}

func (c *Codes) Check(controllerFP, nodeFP, proof string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active() {
		return ErrNoCode
	}
	if hmac.Equal([]byte(Proof(c.code, controllerFP, nodeFP)), []byte(proof)) {
		c.code = ""
		return nil
	}
	c.attempts++
	if c.attempts >= MaxAttempts {
		c.code = ""
		return ErrBurnt
	}
	return ErrBadProof
}

func Proof(code, controllerFP, nodeFP string) string {
	m := hmac.New(sha256.New, []byte(code))
	m.Write([]byte(controllerFP + ":" + nodeFP))
	return hex.EncodeToString(m.Sum(nil))
}

func ValidCode(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./pairing`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pairing
git commit -m "feat(pairing): in-memory 6-digit code with HMAC proof, TTL and attempt limit"
```

---

### Task 3: discovery

**Files:**
- Create: `discovery/discovery.go`, `discovery/discovery_test.go`

**Interfaces:**
- Produces:
  - `discovery.Reply{V int; Nonce, AgentID, Hostname string; Port int; Role, Version, OS, FP string; IP string /*json:"-"*/}`
  - `discovery.DefaultWait = 2*time.Second`
  - `discovery.Listen(addr string, info func() Reply, log *slog.Logger) (*Responder, error)`; `(*Responder).Addr() *net.UDPAddr`; `Close() error`
  - `discovery.Probe(ctx context.Context, targets []*net.UDPAddr, wait time.Duration) ([]Reply, error)` — sorted by hostname, deduped by agent_id
  - `discovery.BroadcastTargets(port int) ([]*net.UDPAddr, error)`

- [ ] **Step 1: Write the failing tests**

`discovery/discovery_test.go`:

```go
package discovery

import (
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

var discard = slog.New(slog.DiscardHandler)

func respond(t *testing.T, r Reply) *Responder {
	t.Helper()
	res, err := Listen("127.0.0.1:0", func() Reply { return r }, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Close() })
	return res
}

func TestProbeFindsResponder(t *testing.T) {
	want := Reply{AgentID: "a1", Hostname: "PC-1", Port: 17891, Role: "node", Version: "0.2.0", OS: "windows", FP: "sha256:ab"}
	res := respond(t, want)
	got, err := Probe(context.Background(), []*net.UDPAddr{res.Addr()}, 300*time.Millisecond)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %+v, %v", got, err)
	}
	g := got[0]
	if g.AgentID != "a1" || g.Hostname != "PC-1" || g.Port != 17891 || g.FP != "sha256:ab" || g.IP != "127.0.0.1" || g.V != 1 {
		t.Errorf("reply %+v", g)
	}
}

func TestProbeDedupesAndSorts(t *testing.T) {
	a := respond(t, Reply{AgentID: "a", Hostname: "zeta"})
	b := respond(t, Reply{AgentID: "b", Hostname: "alpha"})
	got, err := Probe(context.Background(), []*net.UDPAddr{a.Addr(), a.Addr(), b.Addr()}, 300*time.Millisecond)
	if err != nil || len(got) != 2 || got[0].Hostname != "alpha" || got[1].Hostname != "zeta" {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestWrongNonceDropped(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	go func() {
		buf := make([]byte, 512)
		_, from, err := conn.ReadFromUDP(buf)
		if err == nil {
			conn.WriteToUDP([]byte(`{"v":1,"nonce":"0000000000000000","agent_id":"x"}`), from)
		}
	}()
	got, err := Probe(context.Background(), []*net.UDPAddr{conn.LocalAddr().(*net.UDPAddr)}, 300*time.Millisecond)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestResponderIgnoresJunk(t *testing.T) {
	res := respond(t, Reply{AgentID: "a"})
	c, err := net.DialUDP("udp4", nil, res.Addr())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, junk := range []string{"hello", Prefix + "nothex!!nothex!!", Prefix + "abc", Prefix + strings.Repeat("a", 300)} {
		c.Write([]byte(junk))
	}
	c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, err := c.Read(make([]byte, 512)); err == nil {
		t.Fatalf("responder answered junk with %d bytes", n)
	}
}

func TestProbeWithoutTargets(t *testing.T) {
	_, err := Probe(context.Background(), nil, 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "no broadcast-capable IPv4 interface") {
		t.Fatalf("err = %v", err)
	}
}

func TestBroadcastAddr(t *testing.T) {
	tests := []struct{ cidr, want string }{
		{"192.168.1.21/24", "192.168.1.255"},
		{"10.0.5.9/16", "10.0.255.255"},
		{"172.16.3.4/20", "172.16.15.255"},
		{"10.1.1.1/32", ""},
		{"fe80::1/64", ""},
	}
	for _, tt := range tests {
		ip, n, _ := net.ParseCIDR(tt.cidr)
		n.IP = ip
		got := broadcastAddr(n)
		if (got == nil && tt.want != "") || (got != nil && got.String() != tt.want) {
			t.Errorf("%s: got %v, want %q", tt.cidr, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./discovery`
Expected: FAIL to compile — `undefined: Listen`.

- [ ] **Step 3: Implement**

`discovery/discovery.go`:

```go
// Package discovery finds agents by UDP subnet broadcast (blueprint:
// Coordination → Discovery). It is read-only metadata and authorises nothing.
package discovery

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"slices"
	"strings"
	"time"
)

const (
	Prefix      = "SEVJALA/1 DISCOVER "
	DefaultWait = 2 * time.Second
	maxRequest  = 256
	nonceLen    = 16
)

type Reply struct {
	V        int    `json:"v"`
	Nonce    string `json:"nonce"`
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname"`
	Port     int    `json:"port"`
	Role     string `json:"role"`
	Version  string `json:"version"`
	OS       string `json:"os"`
	FP       string `json:"fp"`
	// IP is the reply's source address, set by Probe.
	IP string `json:"-"`
}

type Responder struct {
	conn *net.UDPConn
	done chan struct{}
}

func Listen(addr string, info func() Reply, log *slog.Logger) (*Responder, error) {
	ua, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, fmt.Errorf("discovery %s: %w", addr, err)
	}
	conn, err := net.ListenUDP("udp4", ua)
	if err != nil {
		return nil, fmt.Errorf("discovery %s: %w", addr, err)
	}
	r := &Responder{conn: conn, done: make(chan struct{})}
	go r.serve(info, log)
	return r, nil
}

func (r *Responder) Addr() *net.UDPAddr { return r.conn.LocalAddr().(*net.UDPAddr) }

func (r *Responder) Close() error {
	err := r.conn.Close()
	<-r.done
	return err
}

func (r *Responder) serve(info func() Reply, log *slog.Logger) {
	defer close(r.done)
	buf := make([]byte, maxRequest+1)
	for {
		n, from, err := r.conn.ReadFromUDP(buf)
		if errors.Is(err, net.ErrClosed) {
			return
		}
		if err != nil {
			continue // Windows reports ICMP port-unreachable from earlier replies here
		}
		nonce, ok := parseRequest(buf[:n])
		if !ok {
			continue
		}
		rep := info()
		rep.V, rep.Nonce = 1, nonce
		b, err := json.Marshal(rep)
		if err != nil {
			continue
		}
		if _, err := r.conn.WriteToUDP(b, from); err != nil {
			log.Warn("discovery reply failed", "to", from.String(), "err", err)
		}
	}
}

func parseRequest(b []byte) (string, bool) {
	if len(b) > maxRequest {
		return "", false
	}
	nonce, ok := strings.CutPrefix(string(b), Prefix)
	if !ok || len(nonce) != nonceLen {
		return "", false
	}
	if _, err := hex.DecodeString(nonce); err != nil || strings.ToLower(nonce) != nonce {
		return "", false
	}
	return nonce, true
}

// Probe sends one request to each target and collects replies for wait.
func Probe(ctx context.Context, targets []*net.UDPAddr, wait time.Duration) ([]Reply, error) {
	if len(targets) == 0 {
		return nil, errors.New("no broadcast-capable IPv4 interface is up; check the network cable or add the node with 'sevjala agents add <host:port>'")
	}
	conn, err := net.ListenUDP("udp4", nil)
	if err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	defer conn.Close()
	nb := make([]byte, nonceLen/2)
	if _, err := rand.Read(nb); err != nil {
		return nil, err
	}
	nonce := hex.EncodeToString(nb)
	msg := []byte(Prefix + nonce)
	var sendErr error
	sent := 0
	for _, t := range targets {
		if _, err := conn.WriteToUDP(msg, t); err != nil {
			sendErr = err
			continue
		}
		sent++
	}
	if sent == 0 {
		return nil, fmt.Errorf("discovery: %w", sendErr)
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if err := conn.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	seen := map[string]Reply{}
	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if errors.Is(err, os.ErrDeadlineExceeded) || ctx.Err() != nil {
			break
		}
		if err != nil {
			continue
		}
		var r Reply
		if json.Unmarshal(buf[:n], &r) != nil || r.V != 1 || r.Nonce != nonce || r.AgentID == "" {
			continue
		}
		r.IP = from.IP.String()
		seen[r.AgentID] = r
	}
	out := slices.Collect(maps.Values(seen))
	slices.SortFunc(out, func(a, b Reply) int {
		return cmp.Or(cmp.Compare(a.Hostname, b.Hostname), cmp.Compare(a.AgentID, b.AgentID))
	})
	return out, nil
}

// BroadcastTargets is the subnet broadcast address of every up, broadcast-
// capable, non-loopback IPv4 interface.
func BroadcastTargets(port int) ([]*net.UDPAddr, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []*net.UDPAddr
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagBroadcast == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				if b := broadcastAddr(n); b != nil {
					out = append(out, &net.UDPAddr{IP: b, Port: port})
				}
			}
		}
	}
	return out, nil
}

func broadcastAddr(n *net.IPNet) net.IP {
	ip := n.IP.To4()
	mask := n.Mask
	if len(mask) == net.IPv6len {
		mask = mask[12:]
	}
	if ip == nil || len(mask) != net.IPv4len {
		return nil
	}
	if ones, _ := mask.Size(); ones >= 31 {
		return nil
	}
	b := make(net.IP, net.IPv4len)
	for i := range b {
		b[i] = ip[i] | ^mask[i]
	}
	return b
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./discovery`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add discovery
git commit -m "feat(discovery): UDP broadcast probe and responder with nonce-matched replies"
```

---

### Task 4: lanapi — mTLS server and controller client

**Files:**
- Create: `lanapi/types.go`, `lanapi/server.go`, `lanapi/client.go`, `lanapi/lanapi_test.go`

**Interfaces:**
- Consumes: `identity.Identity`, `identity.Fingerprint`, `identity.LoadController`, `identity.SaveController`, `identity.Peer`, `pairing.Codes`, `pairing.Proof`, `logx.Event*`
- Produces:
  - `lanapi.IdentityInfo{AgentID, Hostname, Role, Version, FP string}`, `PairRequest{ControllerID, ControllerFP, Proof string}`, `PairResponse{AgentID, FP string}`, `Status{AgentID, Role, Version string; UptimeS int64; Health string; Backups []BackupRef; Problems []string}`, `BackupRef{BackupID string; Size int64; SHA256, State string}`
  - `lanapi.Options{Addr, DataDir string; Identity *identity.Identity; Role, Version, Hostname string; Started time.Time; Codes *pairing.Codes; Log *slog.Logger}`
  - `lanapi.Start(Options) (*Server, error)`; `(*Server).Addr() string`, `Port() int`, `Shutdown(ctx) error`
  - `lanapi.NewClient(self *identity.Identity) *Client`; `(*Client).Identity(ctx, addr string) (IdentityInfo, error)`, `Pair(ctx, addr, nodeFP, code string) (PairResponse, error)`, `Status(ctx, addr, nodeFP string) (Status, error)`
  - `lanapi.ErrRejected`; `*lanapi.HTTPError{Code int; Msg string}`

- [ ] **Step 1: Write the failing tests**

`lanapi/lanapi_test.go`:

```go
package lanapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/pairing"
)

var discard = slog.New(slog.DiscardHandler)

type agent struct {
	dir   string
	id    *identity.Identity
	codes *pairing.Codes
	srv   *Server
}

func newID(t *testing.T) (*identity.Identity, string) {
	t.Helper()
	dir := t.TempDir()
	id, _, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	return id, dir
}

func start(t *testing.T, role, dir string, id *identity.Identity, codes *pairing.Codes) *Server {
	t.Helper()
	s, err := Start(Options{Addr: "127.0.0.1:0", DataDir: dir, Identity: id, Role: role, Version: "test",
		Hostname: "host-" + role, Started: time.Now(), Codes: codes, Log: discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	return s
}

func startNode(t *testing.T) agent {
	id, dir := newID(t)
	codes := pairing.New(time.Now)
	return agent{dir: dir, id: id, codes: codes, srv: start(t, "node", dir, id, codes)}
}

func controller(t *testing.T) *Client {
	id, _ := newID(t)
	return NewClient(id)
}

func pair(t *testing.T, c *Client, n agent) {
	t.Helper()
	code, _, _ := n.codes.Issue()
	if _, err := c.Pair(context.Background(), n.srv.Addr(), n.id.FP, code); err != nil {
		t.Fatalf("pair: %v", err)
	}
}

func httpCode(err error) int {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Code
	}
	return 0
}

func TestIdentityWithoutClientCert(t *testing.T) {
	n := startNode(t)
	info, err := controller(t).Identity(context.Background(), n.srv.Addr())
	if err != nil {
		t.Fatal(err)
	}
	if info.AgentID != n.id.AgentID || info.FP != n.id.FP || info.Role != "node" || info.Hostname != "host-node" {
		t.Errorf("identity %+v", info)
	}
}

func TestPairThenStatus(t *testing.T) {
	n := startNode(t)
	c := controller(t)
	pair(t, c, n)
	st, err := c.Status(context.Background(), n.srv.Addr(), n.id.FP)
	if err != nil {
		t.Fatal(err)
	}
	if st.AgentID != n.id.AgentID || st.Health != "healthy" || st.Backups == nil || st.Problems == nil {
		t.Errorf("status %+v", st)
	}
	p, err := identity.LoadController(n.dir)
	if err != nil || p == nil || p.FP != c.self.FP || p.AgentID != c.self.AgentID {
		t.Errorf("saved peer %+v %v", p, err)
	}
}

func TestUnpairedControllerRejectedAtTLS(t *testing.T) {
	n := startNode(t)
	pair(t, controller(t), n)
	_, err := controller(t).Status(context.Background(), n.srv.Addr(), n.id.FP)
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}
	if httpCode(err) != 0 {
		t.Error("rejection reached HTTP")
	}
}

func TestPairFailures(t *testing.T) {
	n := startNode(t)
	c := controller(t)
	ctx := context.Background()

	if _, err := c.Pair(ctx, n.srv.Addr(), n.id.FP, "123456"); !errors.Is(err, ErrRejected) {
		t.Errorf("no active code: err = %v, want a TLS rejection", err)
	}

	code, _, _ := n.codes.Issue()
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if _, err := c.Pair(ctx, n.srv.Addr(), n.id.FP, wrong); httpCode(err) != http.StatusForbidden || !strings.Contains(err.Error(), "wrong pairing code") {
		t.Errorf("wrong code: %v", err)
	}
	if _, err := c.Status(ctx, n.srv.Addr(), n.id.FP); httpCode(err) != http.StatusForbidden {
		t.Errorf("status while only a code is active: %v", err)
	}

	other, _ := newID(t)
	body := PairRequest{ControllerID: other.AgentID, ControllerFP: other.FP, Proof: pairing.Proof(code, other.FP, n.id.FP)}
	var out PairResponse
	err := c.do(ctx, c.http(n.id.FP, true, nil), http.MethodPost, n.srv.Addr(), "/pair", body, &out)
	if httpCode(err) != http.StatusForbidden || !strings.Contains(err.Error(), "does not match") {
		t.Errorf("cert/body fp mismatch: %v", err)
	}
}

func TestStatusWithoutCertIs403(t *testing.T) {
	n := startNode(t)
	c := controller(t)
	var st Status
	err := c.do(context.Background(), c.http(n.id.FP, false, nil), http.MethodGet, n.srv.Addr(), "/status", nil, &st)
	if httpCode(err) != http.StatusForbidden {
		t.Errorf("err = %v", err)
	}
}

func TestRepairReplacesController(t *testing.T) {
	n := startNode(t)
	a, b := controller(t), controller(t)
	pair(t, a, n)
	pair(t, b, n)
	if _, err := b.Status(context.Background(), n.srv.Addr(), n.id.FP); err != nil {
		t.Errorf("new controller: %v", err)
	}
	if _, err := a.Status(context.Background(), n.srv.Addr(), n.id.FP); !errors.Is(err, ErrRejected) {
		t.Errorf("old controller: %v", err)
	}
}

func TestPairingSurvivesRestart(t *testing.T) {
	n := startNode(t)
	c := controller(t)
	pair(t, c, n)
	n.srv.Shutdown(context.Background())
	again := start(t, "node", n.dir, n.id, pairing.New(time.Now))
	if _, err := c.Status(context.Background(), again.Addr(), n.id.FP); err != nil {
		t.Errorf("paired controller after restart: %v", err)
	}
	if _, err := controller(t).Status(context.Background(), again.Addr(), n.id.FP); !errors.Is(err, ErrRejected) {
		t.Errorf("stranger after restart: %v", err)
	}
}

func TestControllerRefusesPair(t *testing.T) {
	id, dir := newID(t)
	s := start(t, "controller", dir, id, nil)
	c := controller(t)
	var out PairResponse
	err := c.do(context.Background(), c.http(id.FP, false, nil), http.MethodPost, s.Addr(), "/pair", PairRequest{}, &out)
	if httpCode(err) != http.StatusConflict {
		t.Errorf("err = %v", err)
	}
}

func TestClientPinsServer(t *testing.T) {
	n := startNode(t)
	pair(t, controller(t), n)
	_, err := controller(t).Status(context.Background(), n.srv.Addr(), "sha256:wrong")
	if err == nil || !strings.Contains(err.Error(), "does not match the pinned") {
		t.Errorf("err = %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./lanapi`
Expected: FAIL to compile — `undefined: Start`.

- [ ] **Step 3: Implement**

`lanapi/types.go`:

```go
// Package lanapi is the agent's HTTPS API on the LAN: mutual TLS with pinned
// fingerprints (blueprint: Coordination → LAN API).
package lanapi

type IdentityInfo struct {
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname"`
	Role     string `json:"role"`
	Version  string `json:"version"`
	FP       string `json:"fp"`
}

type PairRequest struct {
	ControllerID string `json:"controller_id"`
	ControllerFP string `json:"controller_fp"`
	Proof        string `json:"proof"`
}

type PairResponse struct {
	AgentID string `json:"agent_id"`
	FP      string `json:"fp"`
}

type BackupRef struct {
	BackupID string `json:"backup_id"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	State    string `json:"state"`
}

// Status is the M1 subset of the blueprint's GET /status; M2 adds the store.
type Status struct {
	AgentID  string      `json:"agent_id"`
	Role     string      `json:"role"`
	Version  string      `json:"version"`
	UptimeS  int64       `json:"uptime_s"`
	Health   string      `json:"health"`
	Backups  []BackupRef `json:"backups"`
	Problems []string    `json:"problems"`
}
```

`lanapi/server.go`:

```go
package lanapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/pairing"
)

const maxBody = 4 << 10

type Options struct {
	Addr     string
	DataDir  string
	Identity *identity.Identity
	Role     string
	Version  string
	Hostname string
	Started  time.Time
	Codes    *pairing.Codes // nil on a controller
	Log      *slog.Logger
}

type Server struct {
	opt Options
	srv *http.Server
	ln  net.Listener

	mu         sync.Mutex
	controller *identity.Peer
}

var errUnpaired = errors.New("certificate is not the paired controller")

func Start(opt Options) (*Server, error) {
	peer, err := identity.LoadController(opt.DataDir)
	if err != nil {
		return nil, err
	}
	s := &Server{opt: opt, controller: peer}
	base := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{opt.Identity.Cert},
		ClientAuth:   tls.RequestClientCert,
	}
	// A per-connection config lets the rejection log carry the remote address.
	base.GetConfigForClient = func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		c := base.Clone()
		c.GetConfigForClient = nil
		c.VerifyPeerCertificate = s.verifyClient(h.Conn.RemoteAddr().String())
		return c, nil
	}
	ln, err := net.Listen("tcp", opt.Addr)
	if err != nil {
		return nil, fmt.Errorf("lan api %s: %w", opt.Addr, err)
	}
	s.ln = ln
	s.srv = &http.Server{
		Handler:           s.routes(),
		ReadHeaderTimeout: 5 * time.Second,
		ErrorLog:          slog.NewLogLogger(opt.Log.Handler(), slog.LevelDebug),
	}
	go func() {
		if err := s.srv.Serve(tls.NewListener(ln, base)); err != nil && !errors.Is(err, http.ErrServerClosed) {
			opt.Log.Error("lan api stopped", "err", err)
		}
	}()
	return s, nil
}

func (s *Server) Addr() string { return s.ln.Addr().String() }

func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *Server) Shutdown(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	_ = s.ln.Close()
	return err
}

func (s *Server) controllerFP() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.controller == nil {
		return ""
	}
	return s.controller.FP
}

// verifyClient admits no cert, the paired controller, or any cert while a
// pairing code is active; everything else fails the handshake.
func (s *Server) verifyClient(remote string) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 {
			return nil
		}
		fp := identity.Fingerprint(raw[0])
		if fp == s.controllerFP() || (s.opt.Codes != nil && s.opt.Codes.Active()) {
			return nil
		}
		s.opt.Log.Warn("unpaired peer rejected", logx.Event(logx.EventPeerRejected), "fp", fp, "remote", remote)
		return errUnpaired
	}
}

func peerFP(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	return identity.Fingerprint(r.TLS.PeerCertificates[0].Raw)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /identity", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, IdentityInfo{AgentID: s.opt.Identity.AgentID, Hostname: s.opt.Hostname,
			Role: s.opt.Role, Version: s.opt.Version, FP: s.opt.Identity.FP})
	})
	mux.HandleFunc("POST /pair", s.pair)
	mux.HandleFunc("GET /status", s.status)
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	return mux
}

func (s *Server) pair(w http.ResponseWriter, r *http.Request) {
	if s.opt.Codes == nil {
		writeError(w, http.StatusConflict, "this agent is a controller; only nodes can be paired")
		return
	}
	fp := peerFP(r)
	if fp == "" {
		writeError(w, http.StatusForbidden, "client certificate required")
		return
	}
	var req PairRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	if req.ControllerFP != fp {
		s.reject(r, "certificate does not match controller_fp")
		writeError(w, http.StatusForbidden, "client certificate does not match controller_fp")
		return
	}
	if err := s.opt.Codes.Check(req.ControllerFP, s.opt.Identity.FP, req.Proof); err != nil {
		s.reject(r, err.Error())
		writeError(w, http.StatusForbidden, err.Error())
		return
	}
	p := identity.Peer{AgentID: req.ControllerID, FP: fp, PairedAt: time.Now().UTC().Truncate(time.Second)}
	if err := identity.SaveController(s.opt.DataDir, p); err != nil {
		s.opt.Log.Error("save paired controller", "err", err)
		writeError(w, http.StatusInternalServerError, "cannot save the paired controller")
		return
	}
	s.mu.Lock()
	s.controller = &p
	s.mu.Unlock()
	s.opt.Log.Info("paired with controller", logx.Event(logx.EventPairSucceeded),
		"controller_id", p.AgentID, "fp", p.FP, "remote", r.RemoteAddr)
	writeJSON(w, http.StatusOK, PairResponse{AgentID: s.opt.Identity.AgentID, FP: s.opt.Identity.FP})
}

func (s *Server) reject(r *http.Request, reason string) {
	s.opt.Log.Warn("pairing rejected", logx.Event(logx.EventPairRejected), "reason", reason, "remote", r.RemoteAddr)
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if fp := peerFP(r); fp == "" || fp != s.controllerFP() {
		writeError(w, http.StatusForbidden, "not paired with this controller")
		return
	}
	writeJSON(w, http.StatusOK, Status{
		AgentID:  s.opt.Identity.AgentID,
		Role:     s.opt.Role,
		Version:  s.opt.Version,
		UptimeS:  int64(time.Since(s.opt.Started).Seconds()),
		Health:   "healthy",
		Backups:  []BackupRef{},
		Problems: []string{},
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
```

`lanapi/client.go`:

```go
package lanapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/pairing"
)

var ErrRejected = errors.New("rejected this controller's certificate (not paired with it)")

type HTTPError struct {
	Code int
	Msg  string
}

func (e *HTTPError) Error() string { return e.Msg }

// Client is the controller side. It never trusts a CA: every server is pinned
// by fingerprint.
type Client struct {
	self    *identity.Identity
	Timeout time.Duration
}

func NewClient(self *identity.Identity) *Client {
	return &Client{self: self, Timeout: 10 * time.Second}
}

// Identity presents no client cert, so it works against a node paired with
// another controller.
func (c *Client) Identity(ctx context.Context, addr string) (IdentityInfo, error) {
	var tlsFP string
	var info IdentityInfo
	if err := c.do(ctx, c.http("", false, &tlsFP), http.MethodGet, addr, "/identity", nil, &info); err != nil {
		return IdentityInfo{}, err
	}
	if info.FP != tlsFP {
		return IdentityInfo{}, fmt.Errorf("%s: /identity says %s but its certificate is %s", addr, info.FP, tlsFP)
	}
	return info, nil
}

func (c *Client) Pair(ctx context.Context, addr, nodeFP, code string) (PairResponse, error) {
	req := PairRequest{ControllerID: c.self.AgentID, ControllerFP: c.self.FP, Proof: pairing.Proof(code, c.self.FP, nodeFP)}
	var out PairResponse
	err := c.do(ctx, c.http(nodeFP, true, nil), http.MethodPost, addr, "/pair", req, &out)
	return out, err
}

func (c *Client) Status(ctx context.Context, addr, nodeFP string) (Status, error) {
	var st Status
	err := c.do(ctx, c.http(nodeFP, true, nil), http.MethodGet, addr, "/status", nil, &st)
	return st, err
}

// http pins the server to wantFP (any server when empty) and records the
// fingerprint it saw in gotFP.
func (c *Client) http(wantFP string, withCert bool, gotFP *string) *http.Client {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // no CA: VerifyPeerCertificate pins the fingerprint instead
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("server sent no certificate")
			}
			fp := identity.Fingerprint(raw[0])
			if gotFP != nil {
				*gotFP = fp
			}
			if wantFP != "" && fp != wantFP {
				return fmt.Errorf("server certificate %s does not match the pinned %s", fp, wantFP)
			}
			return nil
		},
	}
	if withCert {
		cfg.Certificates = []tls.Certificate{c.self.Cert}
	}
	return &http.Client{Timeout: c.Timeout, Transport: &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}}
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
	resp, err := hc.Do(req)
	if err != nil {
		// The node's alert is not a typed error; this is the only signal.
		if strings.Contains(err.Error(), "tls: bad certificate") {
			return fmt.Errorf("%s %w", addr, ErrRejected)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&e) != nil || e.Error == "" {
			e.Error = resp.Status
		}
		return &HTTPError{Code: resp.StatusCode, Msg: e.Error}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./lanapi`
Expected: PASS (10 tests).

- [ ] **Step 5: Commit**

```bash
git add lanapi
git commit -m "feat(lanapi): mTLS server with pinned controller, /identity /pair /status and client"
```

---

### Task 5: controller store (SQLite)

**Files:**
- Create: `store/store.go`, `store/store_test.go`
- Modify: `go.mod`, `go.sum` (`go get modernc.org/sqlite@v1.60.1`)

**Interfaces:**
- Produces:
  - `store.FileName = "controller.db"`, `store.ErrNotFound`
  - `store.Agent{AgentID, Hostname, LastIP string; Port int; FP, Role, Version string; PairedAt, LastSeen time.Time; State string; FreeBytes int64}`
  - `store.Open(path string) (*Store, error)`; `(*Store).Close() error`, `UpsertAgent(ctx, Agent) error`, `Agents(ctx) ([]Agent, error)` (ordered by hostname, agent_id), `Agent(ctx, id string) (Agent, error)`, `MarkSeen(ctx, id, version string, at time.Time) error`, `UpdateAddress(ctx, id, hostname, ip string, port int) error`

- [ ] **Step 1: Add the dependency**

Run: `go get modernc.org/sqlite@v1.60.1`
Expected: `go: added modernc.org/sqlite v1.60.1`.

- [ ] **Step 2: Write the failing tests**

`store/store_test.go`:

```go
package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func open(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, path
}

var paired = time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)

func agent(id, host string) Agent {
	return Agent{AgentID: id, Hostname: host, LastIP: "10.0.0.2", Port: 17891, FP: "sha256:" + id,
		Role: "node", Version: "0.2.0", PairedAt: paired, State: "online"}
}

func TestOpenTwiceKeepsData(t *testing.T) {
	s, path := open(t)
	if err := s.UpsertAgent(context.Background(), agent("a", "PC-1")); err != nil {
		t.Fatal(err)
	}
	s.Close()
	again, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer again.Close()
	if got, err := again.Agent(context.Background(), "a"); err != nil || got.Hostname != "PC-1" {
		t.Errorf("after reopen: %+v %v", got, err)
	}
}

func TestUpsertListGet(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	for _, a := range []Agent{agent("b", "zeta"), agent("a", "alpha")} {
		if err := s.UpsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	changed := agent("b", "zeta")
	changed.Version = "0.3.0"
	s.UpsertAgent(ctx, changed)

	all, err := s.Agents(ctx)
	if err != nil || len(all) != 2 || all[0].Hostname != "alpha" || all[1].Version != "0.3.0" {
		t.Fatalf("agents %+v %v", all, err)
	}
	got, _ := s.Agent(ctx, "a")
	if !got.PairedAt.Equal(paired) || !got.LastSeen.IsZero() || got.FP != "sha256:a" || got.Port != 17891 {
		t.Errorf("agent a %+v", got)
	}
	if _, err := s.Agent(ctx, "zz"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing agent: %v", err)
	}
}

func TestMarkSeenAndUpdateAddress(t *testing.T) {
	s, _ := open(t)
	ctx := context.Background()
	a := agent("a", "PC-1")
	a.State = "offline"
	s.UpsertAgent(ctx, a)
	seen := paired.Add(time.Hour)
	if err := s.MarkSeen(ctx, "a", "0.4.0", seen); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateAddress(ctx, "a", "PC-1B", "10.0.0.9", 18000); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Agent(ctx, "a")
	if got.Version != "0.4.0" || !got.LastSeen.Equal(seen) || got.State != "online" ||
		got.Hostname != "PC-1B" || got.LastIP != "10.0.0.9" || got.Port != 18000 {
		t.Errorf("agent %+v", got)
	}
	if err := s.MarkSeen(ctx, "zz", "x", seen); !errors.Is(err, ErrNotFound) {
		t.Errorf("MarkSeen missing: %v", err)
	}
	if err := s.UpdateAddress(ctx, "zz", "h", "ip", 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateAddress missing: %v", err)
	}
}

func TestNewerSchemaRefused(t *testing.T) {
	s, path := open(t)
	if _, err := s.db.Exec("PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "newer") {
		t.Errorf("err = %v", err)
	}
}
```

- [ ] **Step 3: Run to verify failure**

Run: `go test ./store`
Expected: FAIL to compile — `undefined: Open`.

- [ ] **Step 4: Implement**

`store/store.go`:

```go
// Package store is the Controller's SQLite state (blueprint: Data model →
// Controller store). Only a controller-role agent opens it.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

const FileName = "controller.db"

var ErrNotFound = errors.New("not found")

// migrations[i] moves the schema from user_version i to i+1.
var migrations = []string{
	`CREATE TABLE agents (
		agent_id   TEXT PRIMARY KEY,
		hostname   TEXT NOT NULL,
		last_ip    TEXT NOT NULL,
		port       INTEGER NOT NULL,
		fp         TEXT NOT NULL,
		role       TEXT NOT NULL,
		version    TEXT NOT NULL,
		paired_at  TEXT NOT NULL,
		last_seen  TEXT NOT NULL DEFAULT '',
		state      TEXT NOT NULL DEFAULT 'online',
		free_bytes INTEGER NOT NULL DEFAULT 0
	)`,
}

type Agent struct {
	AgentID   string
	Hostname  string
	LastIP    string
	Port      int
	FP        string
	Role      string
	Version   string
	PairedAt  time.Time
	LastSeen  time.Time
	State     string
	FreeBytes int64
}

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v > len(migrations) {
		return fmt.Errorf("schema version %d is newer than this build (%d)", v, len(migrations))
	}
	for i := v; i < len(migrations); i++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", i+1)); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

const agentCols = `agent_id, hostname, last_ip, port, fp, role, version, paired_at, last_seen, state, free_bytes`

func (s *Store) UpsertAgent(ctx context.Context, a Agent) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agents (`+agentCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(agent_id) DO UPDATE SET hostname=excluded.hostname, last_ip=excluded.last_ip,
		port=excluded.port, fp=excluded.fp, role=excluded.role, version=excluded.version,
		paired_at=excluded.paired_at, last_seen=excluded.last_seen, state=excluded.state,
		free_bytes=excluded.free_bytes`,
		a.AgentID, a.Hostname, a.LastIP, a.Port, a.FP, a.Role, a.Version,
		fmtTime(a.PairedAt), fmtTime(a.LastSeen), a.State, a.FreeBytes)
	return err
}

func (s *Store) Agents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentCols+` FROM agents ORDER BY hostname, agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Agent(ctx context.Context, id string) (Agent, error) {
	a, err := scanAgent(s.db.QueryRowContext(ctx, `SELECT `+agentCols+` FROM agents WHERE agent_id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Agent{}, fmt.Errorf("agent %s: %w", id, ErrNotFound)
	}
	return a, err
}

func (s *Store) MarkSeen(ctx context.Context, id, version string, at time.Time) error {
	return s.update(ctx, id, `UPDATE agents SET last_seen = ?, version = ?, state = 'online' WHERE agent_id = ?`,
		fmtTime(at), version, id)
}

func (s *Store) UpdateAddress(ctx context.Context, id, hostname, ip string, port int) error {
	return s.update(ctx, id, `UPDATE agents SET hostname = ?, last_ip = ?, port = ? WHERE agent_id = ?`,
		hostname, ip, port, id)
}

func (s *Store) update(ctx context.Context, id, query string, args ...any) error {
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return fmt.Errorf("agent %s: %w", id, ErrNotFound)
	}
	return nil
}

type scanner interface{ Scan(dest ...any) error }

func scanAgent(r scanner) (Agent, error) {
	var a Agent
	var paired, seen string
	if err := r.Scan(&a.AgentID, &a.Hostname, &a.LastIP, &a.Port, &a.FP, &a.Role, &a.Version,
		&paired, &seen, &a.State, &a.FreeBytes); err != nil {
		return Agent{}, err
	}
	var err error
	if a.PairedAt, err = parseTime(paired); err != nil {
		return Agent{}, err
	}
	if a.LastSeen, err = parseTime(seen); err != nil {
		return Agent{}, err
	}
	return a, nil
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}
```

- [ ] **Step 5: Run to verify pass**

Run: `go test -race ./store && GOOS=windows GOARCH=386 go build ./store`
Expected: PASS, and the 386 build succeeds.

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum store
git commit -m "feat(store): controller.db with the agents table and user_version migrations"
```

---

### Task 6: localapi backend routes

**Files:**
- Create: `localapi/api.go`
- Modify: `localapi/server.go` (`NewHandler(b Backend)`), `localapi/client.go` (new methods, 15 s timeout, shared `do`), `localapi/localapi_test.go`

**Interfaces:**
- Produces:
  - `localapi.Status` gains `AgentID string \`json:"agent_id"\``, `FP string \`json:"fp"\``
  - `localapi.Code{Code string; ExpiresAt time.Time; AgentID string}`
  - `localapi.Discovered{AgentID, Hostname, Addr, Role, Version, OS, FP string; Paired bool}`
  - `localapi.Agent{AgentID, Hostname, Addr, Role, Version, FP string; PairedAt, LastSeen time.Time; State string}`
  - `localapi.Ping{Agent Agent; RTTMs, UptimeS int64; Health string}`
  - `localapi.Backend` interface: `Status() Status`, `Code() (Code, error)`, `Discover(ctx) ([]Discovered, error)`, `AddAgent(ctx, addr string) (Discovered, error)`, `Pair(ctx, agent, code string) (Agent, error)`, `Agents(ctx) ([]Agent, error)`, `Ping(ctx, agent string) (Ping, error)`
  - `*localapi.Error{Code int; Msg string}`, `localapi.Errorf(code int, format string, a ...any) error`
  - `localapi.NewHandler(b Backend) http.Handler`
  - `(*Client)` methods: `Status`, `Code`, `Discover`, `AddAgent`, `Pair`, `Agents`, `Ping` mirroring Backend with a leading `ctx`

- [ ] **Step 1: Write the failing tests**

In `localapi/localapi_test.go` replace `startTest` and the `NewHandler(func…)` call in `TestSecondListenerFails` with the fake backend, and add the route tests:

```go
type fakeBackend struct {
	status Status
	err    error

	mu                    sync.Mutex
	gotAgent, gotCode, gotAddr string
}

func (f *fakeBackend) Status() Status { return f.status }

func (f *fakeBackend) Code() (Code, error) {
	return Code{Code: "123456", AgentID: "n1", ExpiresAt: time.Date(2026, 10, 12, 16, 42, 0, 0, time.UTC)}, f.err
}

func (f *fakeBackend) Discover(context.Context) ([]Discovered, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []Discovered{{AgentID: "n1", Addr: "10.0.0.2:17891", Paired: true}}, nil
}

func (f *fakeBackend) AddAgent(_ context.Context, addr string) (Discovered, error) {
	f.mu.Lock()
	f.gotAddr = addr
	f.mu.Unlock()
	return Discovered{AgentID: "n1", Addr: addr}, f.err
}

func (f *fakeBackend) Pair(_ context.Context, agent, code string) (Agent, error) {
	f.mu.Lock()
	f.gotAgent, f.gotCode = agent, code
	f.mu.Unlock()
	return Agent{AgentID: agent}, f.err
}

func (f *fakeBackend) Agents(context.Context) ([]Agent, error) { return nil, f.err }

func (f *fakeBackend) Ping(_ context.Context, agent string) (Ping, error) {
	f.mu.Lock()
	f.gotAgent = agent
	f.mu.Unlock()
	return Ping{Agent: Agent{AgentID: agent}, RTTMs: 3, Health: "healthy"}, f.err
}

func startBackend(t *testing.T, b Backend) string {
	t.Helper()
	ep := Endpoint(testDir(t))
	srv, err := Start(ep, NewHandler(b), discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	return ep
}

func startTest(t *testing.T, st Status) string {
	t.Helper()
	return startBackend(t, &fakeBackend{status: st})
}

func TestRoutesRoundTrip(t *testing.T) {
	f := &fakeBackend{}
	c := NewClient(startBackend(t, f))
	ctx := context.Background()

	if code, err := c.Code(ctx); err != nil || code.Code != "123456" || code.AgentID != "n1" {
		t.Errorf("Code: %+v %v", code, err)
	}
	if ds, err := c.Discover(ctx); err != nil || len(ds) != 1 || !ds[0].Paired {
		t.Errorf("Discover: %+v %v", ds, err)
	}
	if d, err := c.AddAgent(ctx, "10.0.0.9:17891"); err != nil || d.Addr != "10.0.0.9:17891" {
		t.Errorf("AddAgent: %+v %v", d, err)
	}
	if a, err := c.Pair(ctx, "3f2a", "654321"); err != nil || a.AgentID != "3f2a" {
		t.Errorf("Pair: %+v %v", a, err)
	}
	f.mu.Lock()
	if f.gotAgent != "3f2a" || f.gotCode != "654321" || f.gotAddr != "10.0.0.9:17891" {
		t.Errorf("backend got agent=%q code=%q addr=%q", f.gotAgent, f.gotCode, f.gotAddr)
	}
	f.mu.Unlock()
	if as, err := c.Agents(ctx); err != nil || as == nil || len(as) != 0 {
		t.Errorf("Agents: %#v %v (want an empty, non-nil list)", as, err)
	}
	if p, err := c.Ping(ctx, "BACKUP PC/1"); err != nil || p.Agent.AgentID != "BACKUP PC/1" || p.RTTMs != 3 {
		t.Errorf("Ping: %+v %v", p, err)
	}
}

func TestErrorsTravel(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code int
		msg  string
	}{
		{"typed", Errorf(http.StatusConflict, "this agent is a node; run this on the controller"), 409, "this agent is a node; run this on the controller"},
		{"plain", errors.New("disk on fire"), 500, "disk on fire"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewClient(startBackend(t, &fakeBackend{err: tt.err}))
			_, err := c.Discover(context.Background())
			var e *Error
			if !errors.As(err, &e) || e.Code != tt.code || e.Msg != tt.msg {
				t.Errorf("err = %#v", err)
			}
		})
	}
}

func TestBadBodyIs400(t *testing.T) {
	c := NewClient(startBackend(t, &fakeBackend{}))
	resp, err := c.hc.Post("http://sevjala/pair", "application/json", strings.NewReader("{"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status %d", resp.StatusCode)
	}
}
```

Also change `TestSecondListenerFails` to start the second server with `NewHandler(&fakeBackend{})`, and add `"errors"`, `"sync"`, `"time"` to the imports.

- [ ] **Step 2: Run to verify failure**

Run: `go test ./localapi`
Expected: FAIL to compile — `undefined: Backend`, `c.Code undefined`.

- [ ] **Step 3: Implement**

`localapi/api.go`:

```go
package localapi

import (
	"context"
	"fmt"
	"time"
)

type Status struct {
	Version string `json:"version"`
	Role    string `json:"role"`
	PID     int    `json:"pid"`
	UptimeS int64  `json:"uptime_s"`
	DataDir string `json:"data_dir"`
	AgentID string `json:"agent_id"`
	FP      string `json:"fp"`
}

type Code struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	AgentID   string    `json:"agent_id"`
}

type Discovered struct {
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname"`
	Addr     string `json:"addr"`
	Role     string `json:"role"`
	Version  string `json:"version"`
	OS       string `json:"os,omitempty"`
	FP       string `json:"fp"`
	Paired   bool   `json:"paired"`
}

type Agent struct {
	AgentID  string    `json:"agent_id"`
	Hostname string    `json:"hostname"`
	Addr     string    `json:"addr"`
	Role     string    `json:"role"`
	Version  string    `json:"version"`
	FP       string    `json:"fp"`
	PairedAt time.Time `json:"paired_at"`
	LastSeen time.Time `json:"last_seen,omitzero"`
	State    string    `json:"state"`
}

type Ping struct {
	Agent   Agent  `json:"agent"`
	RTTMs   int64  `json:"rtt_ms"`
	UptimeS int64  `json:"uptime_s"`
	Health  string `json:"health"`
}

// Backend is what the agent serves over the pipe; a call for the other role
// returns a 409 *Error.
type Backend interface {
	Status() Status
	Code() (Code, error)
	Discover(ctx context.Context) ([]Discovered, error)
	AddAgent(ctx context.Context, addr string) (Discovered, error)
	Pair(ctx context.Context, agent, code string) (Agent, error)
	Agents(ctx context.Context) ([]Agent, error)
	Ping(ctx context.Context, agent string) (Ping, error)
}

// Error carries an HTTP status across the pipe; Msg is shown to the operator.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Errorf formats like fmt.Sprintf: use %v for errors, there is no wrapping.
func Errorf(code int, format string, a ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

type list[T any] struct {
	Agents []T `json:"agents"`
}

func listOf[T any](xs []T) list[T] {
	if xs == nil {
		xs = []T{}
	}
	return list[T]{Agents: xs}
}
```

`localapi/server.go`: remove the `Status` struct (now in `api.go`), add `"errors"` is already imported, and replace `NewHandler`:

```go
const maxBody = 4 << 10

func NewHandler(b Backend) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, b.Status())
	})
	mux.HandleFunc("POST /code", func(w http.ResponseWriter, _ *http.Request) {
		c, err := b.Code()
		reply(w, c, err)
	})
	mux.HandleFunc("POST /discover", func(w http.ResponseWriter, r *http.Request) {
		ds, err := b.Discover(r.Context())
		reply(w, listOf(ds), err)
	})
	mux.HandleFunc("GET /agents", func(w http.ResponseWriter, r *http.Request) {
		as, err := b.Agents(r.Context())
		reply(w, listOf(as), err)
	})
	mux.HandleFunc("POST /agents", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Addr string `json:"addr"`
		}
		if decode(w, r, &req) {
			d, err := b.AddAgent(r.Context(), req.Addr)
			reply(w, d, err)
		}
	})
	mux.HandleFunc("POST /pair", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Agent string `json:"agent"`
			Code  string `json:"code"`
		}
		if decode(w, r, &req) {
			a, err := b.Pair(r.Context(), req.Agent, req.Code)
			reply(w, a, err)
		}
	})
	mux.HandleFunc("POST /agents/{agent}/ping", func(w http.ResponseWriter, r *http.Request) {
		p, err := b.Ping(r.Context(), r.PathValue("agent"))
		reply(w, p, err)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	return mux
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request: " + err.Error()})
		return false
	}
	return true
}

func reply(w http.ResponseWriter, v any, err error) {
	if err != nil {
		code := http.StatusInternalServerError
		var e *Error
		if errors.As(err, &e) {
			code = e.Code
		}
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, v)
}
```

`localapi/client.go` (whole file):

```go
package localapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

type Client struct {
	hc *http.Client
}

// NewClient's 15 s ceiling covers discover (2 s wait) and a pairing round trip;
// callers pass shorter contexts where they want them.
func NewClient(endpoint string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dial(ctx, endpoint)
		},
	}
	return &Client{hc: &http.Client{Transport: tr, Timeout: 15 * time.Second}}
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var st Status
	return st, c.do(ctx, http.MethodGet, "/status", nil, &st)
}

func (c *Client) Code(ctx context.Context) (Code, error) {
	var code Code
	return code, c.do(ctx, http.MethodPost, "/code", nil, &code)
}

func (c *Client) Discover(ctx context.Context) ([]Discovered, error) {
	var l list[Discovered]
	return l.Agents, c.do(ctx, http.MethodPost, "/discover", nil, &l)
}

func (c *Client) AddAgent(ctx context.Context, addr string) (Discovered, error) {
	var d Discovered
	return d, c.do(ctx, http.MethodPost, "/agents", map[string]string{"addr": addr}, &d)
}

func (c *Client) Pair(ctx context.Context, agent, code string) (Agent, error) {
	var a Agent
	return a, c.do(ctx, http.MethodPost, "/pair", map[string]string{"agent": agent, "code": code}, &a)
}

func (c *Client) Agents(ctx context.Context) ([]Agent, error) {
	var l list[Agent]
	return l.Agents, c.do(ctx, http.MethodGet, "/agents", nil, &l)
}

func (c *Client) Ping(ctx context.Context, agent string) (Ping, error) {
	var p Ping
	return p, c.do(ctx, http.MethodPost, "/agents/"+url.PathEscape(agent)+"/ping", nil, &p)
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://sevjala"+path, rd)
	if err != nil {
		return err
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&e) != nil || e.Error == "" {
			e.Error = fmt.Sprintf("%s %s: %s", method, path, resp.Status)
		}
		return &Error{Code: resp.StatusCode, Msg: e.Error}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}
```

`svc/agent.go` must still compile: replace `localapi.NewHandler(a.status)` with a temporary adapter is not wanted — Task 8 rewires it; for this task wrap it inline:

```go
	api, err := localapi.Start(endpoint, localapi.NewHandler(statusOnly(a.status)), log)
```

and add to `svc/agent.go`:

```go
// statusOnly serves /status and answers every role route with 503 until the
// agent's role parts are running.
type statusOnly func() localapi.Status

func (f statusOnly) Status() localapi.Status { return f() }
func (statusOnly) Code() (localapi.Code, error) { return localapi.Code{}, errStarting }
func (statusOnly) Discover(context.Context) ([]localapi.Discovered, error) { return nil, errStarting }
func (statusOnly) AddAgent(context.Context, string) (localapi.Discovered, error) {
	return localapi.Discovered{}, errStarting
}
func (statusOnly) Pair(context.Context, string, string) (localapi.Agent, error) {
	return localapi.Agent{}, errStarting
}
func (statusOnly) Agents(context.Context) ([]localapi.Agent, error) { return nil, errStarting }
func (statusOnly) Ping(context.Context, string) (localapi.Ping, error) { return localapi.Ping{}, errStarting }

var errStarting = localapi.Errorf(503, "agent is starting")
```

(Task 8 deletes `statusOnly` when `coord.Service` takes over.)

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./localapi ./svc ./cmd`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add localapi svc/agent.go
git commit -m "feat(localapi): Backend interface with code, discover, agents, pair and ping routes"
```

---

### Task 7: coord — role logic and the loopback integration test

**Files:**
- Create: `coord/coord.go`, `coord/controller.go`, `coord/resolve.go`, `coord/coord_test.go`, `coord/integration_test.go`

**Interfaces:**
- Consumes: Tasks 1–6 (`identity`, `pairing`, `discovery.Probe/Reply/Listen`, `lanapi.Client/Start/Options/ErrRejected`, `store.*`, `localapi.*`)
- Produces:
  - `coord.Service{StatusFunc func() localapi.Status; Node *Node; Controller *Controller}` implementing `localapi.Backend`
  - `coord.Node{AgentID string; Codes *pairing.Codes; Log *slog.Logger}`
  - `coord.Controller{Self *identity.Identity; Store *store.Store; LAN *lanapi.Client; Targets func() ([]*net.UDPAddr, error); Wait time.Duration; Log *slog.Logger}` with methods `Discover`, `AddAgent`, `Pair`, `Agents`, `Ping`

- [ ] **Step 1: Write the failing tests**

`coord/coord_test.go`:

```go
package coord

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/jufianto/sevjala/localapi"
)

type item struct{ id, host string }

func key(i item) (string, string) { return i.id, i.host }

func TestResolve(t *testing.T) {
	items := []item{
		{"3f2a9c1e-1111-4111-8111-111111111111", "BACKUP-PC-01"},
		{"3f2a0000-2222-4222-8222-222222222222", "BACKUP-PC-02"},
		{"9a000000-3333-4333-8333-333333333333", "SAMENAME"},
		{"9b000000-4444-4444-8444-444444444444", "samename"},
	}
	tests := []struct {
		name, q, want string
		code          int
	}{
		{"exact id", "3f2a9c1e-1111-4111-8111-111111111111", "BACKUP-PC-01", 0},
		{"hostname any case", "backup-pc-02", "BACKUP-PC-02", 0},
		{"unique prefix", "3f2a9", "BACKUP-PC-01", 0},
		{"prefix upper case", "3F2A0", "BACKUP-PC-02", 0},
		{"ambiguous prefix", "3f2a", "", http.StatusConflict},
		{"ambiguous hostname", "SameName", "", http.StatusConflict},
		{"short prefix", "9a0", "", http.StatusNotFound},
		{"none", "nope", "", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(items, tt.q, key, "hint")
			if tt.code == 0 {
				if err != nil || got.host != tt.want {
					t.Fatalf("got %+v, %v", got, err)
				}
				return
			}
			var e *localapi.Error
			if !errors.As(err, &e) || e.Code != tt.code {
				t.Fatalf("err = %#v, want code %d", err, tt.code)
			}
			if tt.code == http.StatusConflict && !strings.Contains(e.Msg, "9a000000") && !strings.Contains(e.Msg, "3f2a9c1e") {
				t.Errorf("ambiguity does not list candidates: %s", e.Msg)
			}
		})
	}
}

func TestWrongRoleIs409(t *testing.T) {
	node := &Service{Node: &Node{}}
	if _, err := node.Discover(context.Background()); !is409(err, "this agent is a node") {
		t.Errorf("Discover on a node: %v", err)
	}
	ctrl := &Service{Controller: &Controller{}}
	if _, err := ctrl.Code(); !is409(err, "this agent is a controller") {
		t.Errorf("Code on a controller: %v", err)
	}
}

func is409(err error, msg string) bool {
	var e *localapi.Error
	return errors.As(err, &e) && e.Code == http.StatusConflict && strings.Contains(e.Msg, msg)
}
```

`coord/integration_test.go`:

```go
package coord

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/discovery"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/pairing"
	"github.com/jufianto/sevjala/store"
)

var discard = slog.New(slog.DiscardHandler)

type testAgent struct {
	id    *identity.Identity
	codes *pairing.Codes
	lan   *lanapi.Server
	disco *discovery.Responder
}

func startAgent(t *testing.T, role, hostname string) testAgent {
	t.Helper()
	dir := t.TempDir()
	id, _, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	var codes *pairing.Codes
	if role == "node" {
		codes = pairing.New(time.Now)
	}
	lan, err := lanapi.Start(lanapi.Options{Addr: "127.0.0.1:0", DataDir: dir, Identity: id, Role: role,
		Version: "test", Hostname: hostname, Started: time.Now(), Codes: codes, Log: discard})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lan.Shutdown(context.Background()) })
	info := discovery.Reply{AgentID: id.AgentID, Hostname: hostname, Port: lan.Port(), Role: role,
		Version: "test", OS: runtime.GOOS, FP: id.FP}
	disco, err := discovery.Listen("127.0.0.1:0", func() discovery.Reply { return info }, discard)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { disco.Close() })
	return testAgent{id: id, codes: codes, lan: lan, disco: disco}
}

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
	return &Controller{Self: id, Store: st, LAN: lanapi.NewClient(id), Wait: 300 * time.Millisecond, Log: discard,
		Targets: func() ([]*net.UDPAddr, error) {
			var out []*net.UDPAddr
			for _, r := range targets {
				out = append(out, r.Addr())
			}
			return out, nil
		}}
}

func TestDiscoverPairPing(t *testing.T) {
	ctx := context.Background()
	node := startAgent(t, "node", "BACKUP-PC-01")
	ctrl := newController(t, node.disco)

	found, err := ctrl.Discover(ctx)
	if err != nil || len(found) != 1 || found[0].AgentID != node.id.AgentID || found[0].Paired || found[0].Role != "node" {
		t.Fatalf("discover: %+v %v", found, err)
	}

	code, _, _ := node.codes.Issue()
	a, err := ctrl.Pair(ctx, node.id.AgentID[:8], code)
	if err != nil || a.AgentID != node.id.AgentID || a.FP != node.id.FP || a.State != "online" {
		t.Fatalf("pair: %+v %v", a, err)
	}

	p, err := ctrl.Ping(ctx, "backup-pc-01")
	if err != nil || p.Agent.AgentID != node.id.AgentID || p.Health != "healthy" || p.Agent.LastSeen.IsZero() {
		t.Fatalf("ping: %+v %v", p, err)
	}

	agents, err := ctrl.Agents(ctx)
	if err != nil || len(agents) != 1 || agents[0].Hostname != "BACKUP-PC-01" {
		t.Fatalf("agents: %+v %v", agents, err)
	}

	again, _ := ctrl.Discover(ctx)
	if len(again) != 1 || !again[0].Paired {
		t.Errorf("rediscover: %+v", again)
	}

	stranger := newController(t)
	if _, err := stranger.LAN.Status(ctx, node.lan.Addr(), node.id.FP); !errors.Is(err, lanapi.ErrRejected) {
		t.Errorf("unpaired controller: %v, want a TLS rejection", err)
	}
}

func TestPairWrongCode(t *testing.T) {
	ctx := context.Background()
	node := startAgent(t, "node", "PC")
	ctrl := newController(t, node.disco)
	ctrl.Discover(ctx)
	code, _, _ := node.codes.Issue()
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if _, err := ctrl.Pair(ctx, "PC", wrong); err == nil || !strings.Contains(err.Error(), "wrong pairing code") {
		t.Errorf("err = %v", err)
	}
	if as, _ := ctrl.Agents(ctx); len(as) != 0 {
		t.Errorf("stored after a failed pair: %+v", as)
	}
}

func TestPairNeedsDiscovery(t *testing.T) {
	_, err := newController(t).Pair(context.Background(), "abcd", "123456")
	if err == nil || !strings.Contains(err.Error(), "sevjala discover") {
		t.Errorf("err = %v", err)
	}
}

func TestPairRejectsBadCode(t *testing.T) {
	_, err := newController(t).Pair(context.Background(), "abcd", "12")
	if err == nil || !strings.Contains(err.Error(), "6 digits") {
		t.Errorf("err = %v", err)
	}
}

func TestPairRefusesController(t *testing.T) {
	ctx := context.Background()
	other := startAgent(t, "controller", "SERVER-02")
	ctrl := newController(t, other.disco)
	ctrl.Discover(ctx)
	if _, err := ctrl.Pair(ctx, "SERVER-02", "123456"); err == nil || !strings.Contains(err.Error(), "only nodes can be paired") {
		t.Errorf("err = %v", err)
	}
}

func TestAddAgentThenPair(t *testing.T) {
	ctx := context.Background()
	node := startAgent(t, "node", "PC-MANUAL")
	ctrl := newController(t)
	d, err := ctrl.AddAgent(ctx, node.lan.Addr())
	if err != nil || d.AgentID != node.id.AgentID || d.Addr != node.lan.Addr() {
		t.Fatalf("add: %+v %v", d, err)
	}
	code, _, _ := node.codes.Issue()
	if _, err := ctrl.Pair(ctx, "PC-MANUAL", code); err != nil {
		t.Fatalf("pair: %v", err)
	}
}

func TestControllerSkipsItself(t *testing.T) {
	ctx := context.Background()
	ctrl := newController(t)
	info := discovery.Reply{AgentID: ctrl.Self.AgentID, Hostname: "SELF", Role: "controller", FP: ctrl.Self.FP}
	self, err := discovery.Listen("127.0.0.1:0", func() discovery.Reply { return info }, discard)
	if err != nil {
		t.Fatal(err)
	}
	defer self.Close()
	ctrl.Targets = func() ([]*net.UDPAddr, error) { return []*net.UDPAddr{self.Addr()}, nil }
	if found, err := ctrl.Discover(ctx); err != nil || len(found) != 0 {
		t.Errorf("found %+v, %v", found, err)
	}
}

func TestDiscoverRefreshesPairedAddress(t *testing.T) {
	ctx := context.Background()
	node := startAgent(t, "node", "PC-DHCP")
	ctrl := newController(t, node.disco)
	ctrl.Discover(ctx)
	code, _, _ := node.codes.Issue()
	if _, err := ctrl.Pair(ctx, "PC-DHCP", code); err != nil {
		t.Fatal(err)
	}
	if err := ctrl.Store.UpdateAddress(ctx, node.id.AgentID, "PC-DHCP", "10.255.255.1", 1); err != nil {
		t.Fatal(err)
	}
	ctrl.Discover(ctx)
	if _, err := ctrl.Ping(ctx, "PC-DHCP"); err != nil {
		t.Errorf("ping after rediscovery: %v", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./coord`
Expected: FAIL to compile — `undefined: resolve`, `undefined: Service`.

- [ ] **Step 3: Implement**

`coord/coord.go`:

```go
// Package coord is the role logic behind the local API: a node issues pairing
// codes, a controller discovers, pairs and pings nodes.
package coord

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/pairing"
)

// Service holds exactly one of Node or Controller, by role.
type Service struct {
	StatusFunc func() localapi.Status
	Node       *Node
	Controller *Controller
}

var _ localapi.Backend = (*Service)(nil)

var (
	errNotNode       = localapi.Errorf(http.StatusConflict, "this agent is a controller; run this on a node")
	errNotController = localapi.Errorf(http.StatusConflict, "this agent is a node; run this on the controller")
)

func (s *Service) Status() localapi.Status { return s.StatusFunc() }

func (s *Service) Code() (localapi.Code, error) {
	if s.Node == nil {
		return localapi.Code{}, errNotNode
	}
	return s.Node.Code()
}

func (s *Service) Discover(ctx context.Context) ([]localapi.Discovered, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	return s.Controller.Discover(ctx)
}

func (s *Service) AddAgent(ctx context.Context, addr string) (localapi.Discovered, error) {
	if s.Controller == nil {
		return localapi.Discovered{}, errNotController
	}
	return s.Controller.AddAgent(ctx, addr)
}

func (s *Service) Pair(ctx context.Context, agent, code string) (localapi.Agent, error) {
	if s.Controller == nil {
		return localapi.Agent{}, errNotController
	}
	return s.Controller.Pair(ctx, agent, code)
}

func (s *Service) Agents(ctx context.Context) ([]localapi.Agent, error) {
	if s.Controller == nil {
		return nil, errNotController
	}
	return s.Controller.Agents(ctx)
}

func (s *Service) Ping(ctx context.Context, agent string) (localapi.Ping, error) {
	if s.Controller == nil {
		return localapi.Ping{}, errNotController
	}
	return s.Controller.Ping(ctx, agent)
}

type Node struct {
	AgentID string
	Codes   *pairing.Codes
	Log     *slog.Logger
}

func (n *Node) Code() (localapi.Code, error) {
	code, exp, err := n.Codes.Issue()
	if err != nil {
		return localapi.Code{}, err
	}
	n.Log.Info("pairing code issued", logx.Event(logx.EventPairCodeIssued), "expires_at", exp)
	return localapi.Code{Code: code, ExpiresAt: exp, AgentID: n.AgentID}, nil
}
```

`coord/resolve.go`:

```go
package coord

import (
	"net/http"
	"strings"

	"github.com/jufianto/sevjala/localapi"
)

const minPrefix = 4

// resolve picks one item by exact agent_id, else hostname (any case), else a
// unique agent_id prefix of at least minPrefix characters.
func resolve[T any](items []T, q string, key func(T) (id, host string), hint string) (T, error) {
	var zero T
	q = strings.TrimSpace(q)
	for _, it := range items {
		if id, _ := key(it); id == q {
			return it, nil
		}
	}
	var hits []T
	for _, it := range items {
		if _, host := key(it); strings.EqualFold(host, q) {
			hits = append(hits, it)
		}
	}
	if len(hits) == 0 && len(q) >= minPrefix {
		for _, it := range items {
			if id, _ := key(it); strings.HasPrefix(id, strings.ToLower(q)) {
				hits = append(hits, it)
			}
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return zero, localapi.Errorf(http.StatusNotFound, "no agent matches %q; %s", q, hint)
	}
	names := make([]string, len(hits))
	for i, it := range hits {
		id, host := key(it)
		names[i] = host + " (" + id + ")"
	}
	return zero, localapi.Errorf(http.StatusConflict, "%q matches %d agents: %s; use more of the agent id",
		q, len(hits), strings.Join(names, ", "))
}
```

`coord/controller.go`:

```go
package coord

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/discovery"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/pairing"
	"github.com/jufianto/sevjala/store"
)

type Controller struct {
	Self    *identity.Identity
	Store   *store.Store
	LAN     *lanapi.Client
	Targets func() ([]*net.UDPAddr, error)
	Wait    time.Duration
	Log     *slog.Logger

	mu   sync.Mutex
	seen map[string]localapi.Discovered // from discover and agents add; pair needs the fp seen here
}

const (
	seenHint   = "run 'sevjala discover' or 'sevjala agents add <host:port>' first"
	pairedHint = "see 'sevjala agents' for paired agents"
)

func (c *Controller) Discover(ctx context.Context) ([]localapi.Discovered, error) {
	targets, err := c.Targets()
	if err != nil {
		return nil, err
	}
	replies, err := discovery.Probe(ctx, targets, c.Wait)
	if err != nil {
		return nil, err
	}
	out := make([]localapi.Discovered, 0, len(replies))
	for _, r := range replies {
		if r.AgentID == c.Self.AgentID {
			continue
		}
		d := localapi.Discovered{AgentID: r.AgentID, Hostname: r.Hostname, Addr: net.JoinHostPort(r.IP, strconv.Itoa(r.Port)),
			Role: r.Role, Version: r.Version, OS: r.OS, FP: r.FP}
		a, err := c.Store.Agent(ctx, r.AgentID)
		switch {
		case err == nil && a.FP == r.FP:
			d.Paired = true
			if err := c.Store.UpdateAddress(ctx, a.AgentID, r.Hostname, r.IP, r.Port); err != nil {
				return nil, err
			}
		case err != nil && !errors.Is(err, store.ErrNotFound):
			return nil, err
		}
		out = append(out, d)
	}
	c.remember(out...)
	c.Log.Info("discovery run", logx.Event(logx.EventDiscoveryRun), "found", len(out), "targets", len(targets))
	return out, nil
}

func (c *Controller) AddAgent(ctx context.Context, addr string) (localapi.Discovered, error) {
	if _, _, err := net.SplitHostPort(addr); err != nil {
		addr = net.JoinHostPort(addr, strconv.Itoa(config.DefaultLANPort))
	}
	info, err := c.LAN.Identity(ctx, addr)
	if err != nil {
		return localapi.Discovered{}, localapi.Errorf(http.StatusBadGateway, "%s: %v", addr, err)
	}
	if info.AgentID == c.Self.AgentID {
		return localapi.Discovered{}, localapi.Errorf(http.StatusBadRequest, "%s is this controller", addr)
	}
	d := localapi.Discovered{AgentID: info.AgentID, Hostname: info.Hostname, Addr: addr, Role: info.Role,
		Version: info.Version, FP: info.FP}
	a, err := c.Store.Agent(ctx, info.AgentID)
	switch {
	case err == nil:
		d.Paired = a.FP == info.FP
	case !errors.Is(err, store.ErrNotFound):
		return localapi.Discovered{}, err
	}
	c.remember(d)
	c.Log.Info("agent added", logx.Event(logx.EventAgentAdded), "agent_id", d.AgentID, "addr", addr)
	return d, nil
}

func (c *Controller) Pair(ctx context.Context, agent, code string) (localapi.Agent, error) {
	if !pairing.ValidCode(code) {
		return localapi.Agent{}, localapi.Errorf(http.StatusBadRequest, "the code must be the 6 digits shown by 'sevjala code' on the node")
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
	if _, err := c.LAN.Pair(ctx, d.Addr, d.FP, code); err != nil {
		c.Log.Warn("pairing failed", logx.Event(logx.EventPairRejected), "agent_id", d.AgentID, "addr", d.Addr, "err", err)
		return localapi.Agent{}, localapi.Errorf(http.StatusBadGateway, "pair %s: %v", d.Hostname, err)
	}
	host, portStr, _ := net.SplitHostPort(d.Addr)
	port, _ := strconv.Atoi(portStr)
	now := time.Now().UTC().Truncate(time.Second)
	a := store.Agent{AgentID: d.AgentID, Hostname: d.Hostname, LastIP: host, Port: port, FP: d.FP,
		Role: d.Role, Version: d.Version, PairedAt: now, LastSeen: now, State: "online"}
	if err := c.Store.UpsertAgent(ctx, a); err != nil {
		return localapi.Agent{}, err
	}
	d.Paired = true
	c.remember(d)
	c.Log.Info("paired with node", logx.Event(logx.EventPairSucceeded), "agent_id", a.AgentID, "hostname", a.Hostname, "fp", a.FP)
	return toAPI(a), nil
}

func (c *Controller) Agents(ctx context.Context) ([]localapi.Agent, error) {
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]localapi.Agent, len(as))
	for i, a := range as {
		out[i] = toAPI(a)
	}
	return out, nil
}

func (c *Controller) Ping(ctx context.Context, agent string) (localapi.Ping, error) {
	as, err := c.Store.Agents(ctx)
	if err != nil {
		return localapi.Ping{}, err
	}
	a, err := resolve(as, agent, func(a store.Agent) (string, string) { return a.AgentID, a.Hostname }, pairedHint)
	if err != nil {
		return localapi.Ping{}, err
	}
	addr := net.JoinHostPort(a.LastIP, strconv.Itoa(a.Port))
	start := time.Now()
	st, err := c.LAN.Status(ctx, addr, a.FP)
	if err != nil {
		return localapi.Ping{}, localapi.Errorf(http.StatusBadGateway, "%s (%s): %v", a.Hostname, addr, err)
	}
	rtt := time.Since(start)
	now := time.Now().UTC().Truncate(time.Second)
	if err := c.Store.MarkSeen(ctx, a.AgentID, st.Version, now); err != nil {
		return localapi.Ping{}, err
	}
	a.Version, a.LastSeen, a.State = st.Version, now, "online"
	return localapi.Ping{Agent: toAPI(a), RTTMs: rtt.Milliseconds(), UptimeS: st.UptimeS, Health: st.Health}, nil
}

func (c *Controller) remember(ds ...localapi.Discovered) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seen == nil {
		c.seen = map[string]localapi.Discovered{}
	}
	for _, d := range ds {
		c.seen[d.AgentID] = d
	}
}

func toAPI(a store.Agent) localapi.Agent {
	return localapi.Agent{AgentID: a.AgentID, Hostname: a.Hostname, Addr: net.JoinHostPort(a.LastIP, strconv.Itoa(a.Port)),
		Role: a.Role, Version: a.Version, FP: a.FP, PairedAt: a.PairedAt, LastSeen: a.LastSeen, State: a.State}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./coord`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add coord
git commit -m "feat(coord): controller discover/add/pair/ping and node code behind the local API"
```

---

### Task 8: agent runtime wiring

**Files:**
- Modify: `svc/agent.go` (whole file), `svc/agent_test.go`

**Interfaces:**
- Consumes: everything above; `coord.Service`, `discovery.BroadcastTargets`, `discovery.DefaultWait`
- Produces: `svc.Agent` starting identity → store (controller) → local API → LAN API → discovery responder; `localapi.Status.AgentID/FP` filled

- [ ] **Step 1: Write the failing tests**

In `svc/agent_test.go` add a config helper and use it in both existing tests (replacing `config.Default(config.RoleNode)`), extend the first test, and add a controller test:

```go
// testConfig keeps tests off the real ports and off other interfaces.
func testConfig(role string) config.Config {
	c := config.Default(role)
	c.Listen = "127.0.0.1:0"
	c.DiscoveryPort = 0
	return c
}
```

In `TestAgentStartServesStatusAndLogs` after the existing status assertions add:

```go
	if st.AgentID == "" || !strings.HasPrefix(st.FP, "sha256:") {
		t.Errorf("status lacks identity: %+v", st)
	}
	if _, err := localapi.NewClient(localapi.Endpoint(dir)).Discover(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "this agent is a node") {
		t.Errorf("discover on a node: %v", err)
	}
```

and add `logx.EventIdentityCreated` to the event loop's list. New test:

```go
func TestControllerAgentOpensStore(t *testing.T) {
	dir := shortDir(t)
	a := &Agent{DataDir: dir, Config: testConfig(config.RoleController), Version: "test"}
	if err := a.Start(nil); err != nil {
		t.Fatal(err)
	}
	defer a.Stop(nil)
	c := localapi.NewClient(localapi.Endpoint(dir))
	if as, err := c.Agents(context.Background()); err != nil || len(as) != 0 {
		t.Errorf("agents: %+v %v", as, err)
	}
	if _, err := os.Stat(filepath.Join(dir, store.FileName)); err != nil {
		t.Errorf("controller.db: %v", err)
	}
	if _, err := c.Code(context.Background()); err == nil || !strings.Contains(err.Error(), "this agent is a controller") {
		t.Errorf("code on a controller: %v", err)
	}
}
```

(import `github.com/jufianto/sevjala/store`.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./svc`
Expected: FAIL — status lacks identity / discover answers 503 "agent is starting" / `controller.db` missing.

- [ ] **Step 3: Implement**

`svc/agent.go` (whole file; `statusOnly` from Task 6 is gone):

```go
package svc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/kardianos/service"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/coord"
	"github.com/jufianto/sevjala/discovery"
	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/lanapi"
	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/pairing"
	"github.com/jufianto/sevjala/store"
)

type Agent struct {
	DataDir    string
	Config     config.Config
	Version    string
	Foreground bool

	log     *slog.Logger
	logFile io.Closer
	started time.Time
	id      *identity.Identity
	store   *store.Store
	api     *localapi.Server
	lan     *lanapi.Server
	disco   *discovery.Responder
}

// Start must not block: kardianos/service calls it on the service thread.
func (a *Agent) Start(service.Service) error {
	log, closer, err := logx.Open(config.LogDir(a.DataDir), a.Foreground)
	if err != nil {
		return err
	}
	a.log, a.logFile, a.started = log, closer, time.Now()
	if err := a.start(); err != nil {
		log.Error("agent failed to start", "err", err)
		_ = a.shutdown()
		closer.Close()
		a.logFile = nil
		return err
	}
	log.Info("agent started", logx.Event(logx.EventAgentStarted), "role", a.Config.Role, "version", a.Version,
		"agent_id", a.id.AgentID, "lan", a.lan.Addr(), "discovery", a.disco.Addr().String(), "data_dir", a.DataDir)
	return nil
}

func (a *Agent) start() error {
	id, created, err := identity.LoadOrCreate(a.DataDir)
	if err != nil {
		return err
	}
	a.id = id
	if created {
		a.log.Info("identity created", logx.Event(logx.EventIdentityCreated), "agent_id", id.AgentID, "fp", id.FP)
	}
	hostname, _ := os.Hostname()
	backend := &coord.Service{StatusFunc: a.status}
	var codes *pairing.Codes
	switch a.Config.Role {
	case config.RoleNode:
		codes = pairing.New(time.Now)
		backend.Node = &coord.Node{AgentID: id.AgentID, Codes: codes, Log: a.log}
	case config.RoleController:
		if a.store, err = store.Open(filepath.Join(a.DataDir, store.FileName)); err != nil {
			return err
		}
		port := a.Config.DiscoveryPort
		backend.Controller = &coord.Controller{Self: id, Store: a.store, LAN: lanapi.NewClient(id),
			Targets: func() ([]*net.UDPAddr, error) { return discovery.BroadcastTargets(port) },
			Wait:    discovery.DefaultWait, Log: a.log}
	}
	if a.api, err = localapi.Start(localapi.Endpoint(a.DataDir), localapi.NewHandler(backend), a.log); err != nil {
		return err
	}
	if a.lan, err = lanapi.Start(lanapi.Options{Addr: a.Config.Listen, DataDir: a.DataDir, Identity: id,
		Role: a.Config.Role, Version: a.Version, Hostname: hostname, Started: a.started, Codes: codes, Log: a.log}); err != nil {
		return err
	}
	info := discovery.Reply{AgentID: id.AgentID, Hostname: hostname, Port: a.lan.Port(), Role: a.Config.Role,
		Version: a.Version, OS: runtime.GOOS, FP: id.FP}
	a.disco, err = discovery.Listen(fmt.Sprintf("0.0.0.0:%d", a.Config.DiscoveryPort), func() discovery.Reply { return info }, a.log)
	return err
}

// shutdown stops whatever start got to, in reverse order.
func (a *Agent) shutdown() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var errs []error
	if a.disco != nil {
		errs = append(errs, a.disco.Close())
		a.disco = nil
	}
	if a.lan != nil {
		errs = append(errs, a.lan.Shutdown(ctx))
		a.lan = nil
	}
	if a.api != nil {
		errs = append(errs, a.api.Shutdown(ctx))
		a.api = nil
	}
	if a.store != nil {
		errs = append(errs, a.store.Close())
		a.store = nil
	}
	return errors.Join(errs...)
}

func (a *Agent) Stop(service.Service) error {
	if a.logFile == nil {
		return nil
	}
	err := a.shutdown()
	a.log.Info("agent stopped", logx.Event(logx.EventAgentStopped))
	err = errors.Join(err, a.logFile.Close())
	a.logFile = nil
	return err
}

func (a *Agent) status() localapi.Status {
	return localapi.Status{
		Version: a.Version,
		Role:    a.Config.Role,
		PID:     os.Getpid(),
		UptimeS: int64(time.Since(a.started).Seconds()),
		DataDir: a.DataDir,
		AgentID: a.id.AgentID,
		FP:      a.id.FP,
	}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./svc ./cmd`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add svc/agent.go svc/agent_test.go
git commit -m "feat(svc): agent runs identity, store, LAN API and discovery by role"
```

---

### Task 9: CLI — discover, code, pair, agents, ping

**Files:**
- Create: `cmd/coord.go`, `cmd/coord_test.go`
- Modify: `cmd/root.go` (register commands)

**Interfaces:**
- Consumes: `localapi.Client` methods, `pairing.ValidCode`
- Produces: commands `discover`, `code`, `pair <agent> --code N`, `agents`, `agents add <host:port>`, `ping <agent>`; renderers `renderDiscovered`, `renderCode`, `renderAgents`, `renderPing`, helper `shortID`

- [ ] **Step 1: Write the failing tests**

`cmd/coord_test.go`:

```go
package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/localapi"
)

func TestRenderDiscovered(t *testing.T) {
	var b bytes.Buffer
	renderDiscovered(&b, []localapi.Discovered{
		{AgentID: "3f2a9c1e-1111-4111-8111-111111111111", Hostname: "BACKUP-PC-01", Addr: "192.168.1.21:17891", Role: "node", Version: "0.2.0", Paired: true},
		{AgentID: "9b000000-4444-4444-8444-444444444444", Hostname: "BACKUP-PC-02", Addr: "192.168.1.22:17891", Role: "node", Version: "0.2.0"},
	})
	want := "AGENT     HOSTNAME      ADDRESS             ROLE  VERSION  PAIRED\n" +
		"3f2a9c1e  BACKUP-PC-01  192.168.1.21:17891  node  0.2.0    yes\n" +
		"9b000000  BACKUP-PC-02  192.168.1.22:17891  node  0.2.0    no\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
	b.Reset()
	renderDiscovered(&b, nil)
	if !strings.Contains(b.String(), "sevjala agents add") {
		t.Errorf("empty: %q", b.String())
	}
}

func TestRenderCode(t *testing.T) {
	var b bytes.Buffer
	renderCode(&b, localapi.Code{Code: "123456", AgentID: "3f2a9c1e-1111", ExpiresAt: time.Date(2026, 10, 12, 16, 42, 0, 0, time.Local)})
	want := "pairing code 123456, valid until 16:42\non the controller run: sevjala pair 3f2a9c1e --code 123456\n"
	if b.String() != want {
		t.Errorf("got %q", b.String())
	}
}

func TestRenderAgents(t *testing.T) {
	var b bytes.Buffer
	seen := time.Date(2026, 10, 12, 16, 30, 0, 0, time.Local)
	renderAgents(&b, []localapi.Agent{
		{AgentID: "3f2a9c1e-1111", Hostname: "BACKUP-PC-01", Addr: "192.168.1.21:17891", State: "online", LastSeen: seen},
		{AgentID: "9b000000-4444", Hostname: "BACKUP-PC-02", Addr: "192.168.1.22:17891", State: "online"},
	})
	want := "AGENT     HOSTNAME      ADDRESS             STATE   LAST SEEN\n" +
		"3f2a9c1e  BACKUP-PC-01  192.168.1.21:17891  online  2026-10-12 16:30\n" +
		"9b000000  BACKUP-PC-02  192.168.1.22:17891  online  never\n"
	if b.String() != want {
		t.Errorf("got\n%s\nwant\n%s", b.String(), want)
	}
	b.Reset()
	renderAgents(&b, nil)
	if !strings.Contains(b.String(), "no paired agents") {
		t.Errorf("empty: %q", b.String())
	}
}

func TestRenderPing(t *testing.T) {
	var b bytes.Buffer
	renderPing(&b, localapi.Ping{Agent: localapi.Agent{Hostname: "BACKUP-PC-01", Addr: "192.168.1.21:17891", Role: "node", Version: "0.2.0"},
		RTTMs: 4, UptimeS: 7380, Health: "healthy"})
	want := "BACKUP-PC-01 (192.168.1.21:17891) answered in 4 ms: node 0.2.0, healthy, up 2h3m0s\n"
	if b.String() != want {
		t.Errorf("got %q", b.String())
	}
}

func TestCoordUsageErrors(t *testing.T) {
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"pair", "abcd", "--code", "12"}, "6-digit"},
		{[]string{"pair", "abcd"}, "6-digit"},
		{[]string{"pair", "--code", "123456"}, "accepts 1 arg"},
		{[]string{"ping"}, "accepts 1 arg"},
		{[]string{"agents", "add"}, "accepts 1 arg"},
	}
	for _, tt := range tests {
		var out, errOut bytes.Buffer
		if code := run(tt.args, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), tt.want) {
			t.Errorf("%v: exit %d, stderr %q", tt.args, code, errOut.String())
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd`
Expected: FAIL to compile — `undefined: renderDiscovered`.

- [ ] **Step 3: Implement**

`cmd/coord.go`:

```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/jufianto/sevjala/localapi"
	"github.com/jufianto/sevjala/pairing"
)

// callAgent runs one local API call; an answer with an error is the agent's
// message, anything else means the agent is not reachable.
func callAgent[T any](g *globals, timeout time.Duration, f func(context.Context, *localapi.Client) (T, error)) (T, error) {
	var zero T
	dataDir, err := g.dataDir()
	if err != nil {
		return zero, fail(err)
	}
	ep := localapi.Endpoint(dataDir)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	v, err := f(ctx, localapi.NewClient(ep))
	if err != nil {
		var le *localapi.Error
		if errors.As(err, &le) {
			return zero, fail(le)
		}
		return zero, failf("agent not answering at %s (is the service running?): %w", ep, err)
	}
	return v, nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func newDiscoverCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "discover",
		Short: "Find agents on the LAN (controller)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			ds, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.Discovered, error) {
				return cl.Discover(ctx)
			})
			if err != nil {
				return err
			}
			renderDiscovered(c.OutOrStdout(), ds)
			return nil
		},
	}
}

func renderDiscovered(w io.Writer, ds []localapi.Discovered) {
	if len(ds) == 0 {
		fmt.Fprintln(w, "no agents answered; check the firewall on the nodes, or add one with 'sevjala agents add <host:port>'")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tHOSTNAME\tADDRESS\tROLE\tVERSION\tPAIRED")
	for _, d := range ds {
		paired := "no"
		if d.Paired {
			paired = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", shortID(d.AgentID), d.Hostname, d.Addr, d.Role, d.Version, paired)
	}
	tw.Flush()
}

func newCodeCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "code",
		Short: "Show a one-time pairing code (node)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			code, err := callAgent(g, 5*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Code, error) {
				return cl.Code(ctx)
			})
			if err != nil {
				return err
			}
			renderCode(c.OutOrStdout(), code)
			return nil
		},
	}
}

func renderCode(w io.Writer, c localapi.Code) {
	fmt.Fprintf(w, "pairing code %s, valid until %s\n", c.Code, c.ExpiresAt.Local().Format("15:04"))
	fmt.Fprintf(w, "on the controller run: sevjala pair %s --code %s\n", shortID(c.AgentID), c.Code)
}

func newPairCmd(g *globals) *cobra.Command {
	var code string
	c := &cobra.Command{
		Use:   "pair <agent> --code <code>",
		Short: "Pair with a discovered node using its code (controller)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			if !pairing.ValidCode(code) {
				return usagef("--code must be the 6-digit code shown by 'sevjala code' on the node")
			}
			a, err := callAgent(g, 20*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Agent, error) {
				return cl.Pair(ctx, args[0], code)
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(c.OutOrStdout(), "paired with %s (%s) %s\n", a.Hostname, shortID(a.AgentID), a.FP)
			return nil
		},
	}
	c.Flags().StringVar(&code, "code", "", "6-digit code from 'sevjala code' on the node")
	return c
}

func newAgentsCmd(g *globals) *cobra.Command {
	c := &cobra.Command{
		Use:   "agents",
		Short: "List paired agents (controller)",
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			as, err := callAgent(g, 5*time.Second, func(ctx context.Context, cl *localapi.Client) ([]localapi.Agent, error) {
				return cl.Agents(ctx)
			})
			if err != nil {
				return err
			}
			renderAgents(c.OutOrStdout(), as)
			return nil
		},
	}
	c.AddCommand(&cobra.Command{
		Use:   "add <host[:port]>",
		Short: "Add a node by address when discovery cannot reach it (controller)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			d, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Discovered, error) {
				return cl.AddAgent(ctx, args[0])
			})
			if err != nil {
				return err
			}
			out := c.OutOrStdout()
			renderDiscovered(out, []localapi.Discovered{d})
			if !d.Paired {
				fmt.Fprintf(out, "pair it with: sevjala pair %s --code <code from 'sevjala code' on that PC>\n", shortID(d.AgentID))
			}
			return nil
		},
	})
	return c
}

func renderAgents(w io.Writer, as []localapi.Agent) {
	if len(as) == 0 {
		fmt.Fprintln(w, "no paired agents; run 'sevjala discover', then 'sevjala pair <agent> --code <code>'")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AGENT\tHOSTNAME\tADDRESS\tSTATE\tLAST SEEN")
	for _, a := range as {
		seen := "never"
		if !a.LastSeen.IsZero() {
			seen = a.LastSeen.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", shortID(a.AgentID), a.Hostname, a.Addr, a.State, seen)
	}
	tw.Flush()
}

func newPingCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "ping <agent>",
		Short: "Check a paired node over mutual TLS (controller)",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			p, err := callAgent(g, 15*time.Second, func(ctx context.Context, cl *localapi.Client) (localapi.Ping, error) {
				return cl.Ping(ctx, args[0])
			})
			if err != nil {
				return err
			}
			renderPing(c.OutOrStdout(), p)
			return nil
		},
	}
}

func renderPing(w io.Writer, p localapi.Ping) {
	a := p.Agent
	fmt.Fprintf(w, "%s (%s) answered in %d ms: %s %s, %s, up %s\n", a.Hostname, a.Addr, p.RTTMs, a.Role, a.Version,
		p.Health, time.Duration(p.UptimeS)*time.Second)
}
```

`cmd/root.go`: register the new commands:

```go
	root.AddCommand(newVersionCmd(), newAgentCmd(g), newServiceCmd(g), newDoctorCmd(g),
		newDiscoverCmd(g), newCodeCmd(g), newPairCmd(g), newAgentsCmd(g), newPingCmd(g))
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./cmd`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/coord.go cmd/coord_test.go cmd/root.go
git commit -m "feat(cmd): discover, code, pair, agents and ping commands"
```

---

### Task 10: data dir lockdown, firewall, install/uninstall, doctor

**Files:**
- Create: `svc/lock_unix.go`, `svc/lock_windows.go`, `svc/lock_unix_test.go`, `svc/firewall.go`, `svc/firewall_test.go`
- Modify: `cmd/service.go` (install, uninstall), `cmd/doctor.go`, `cmd/doctor_test.go`

**Interfaces:**
- Consumes: `identity.Load`, `lanapi.NewClient(...).Identity`, `config.(Config).ListenPort`
- Produces: `svc.LockDataDir(dir string) error`, `svc.CheckDataDirLocked(dir string) error`, `svc.AddFirewallRules(tcpPort, udpPort int) error`, `svc.RemoveFirewallRules()`, `svc.FirewallRulesPresent() error`, `svc.FirewallRuleLAN`, `svc.FirewallRuleDiscovery`; doctor checks `lockdown`, `identity`, `lan api`, `firewall`

- [ ] **Step 1: Write the failing tests**

`svc/lock_unix_test.go`:

```go
//go:build !windows

package svc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckDataDirLockedRejectsOpenDir(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o755)
	if err := CheckDataDirLocked(dir); err == nil {
		t.Error("a 0755 dir (or one not owned by root) passed")
	}
}

func TestLockDataDirRefusesSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "real")
	os.Mkdir(target, 0o700)
	link := filepath.Join(base, "data")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := LockDataDir(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("err = %v", err)
	}
}

func TestLockDataDirAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	dir := filepath.Join(t.TempDir(), "data")
	if err := LockDataDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := CheckDataDirLocked(dir); err != nil {
		t.Error(err)
	}
	foreign := filepath.Join(t.TempDir(), "planted")
	os.Mkdir(foreign, 0o755)
	os.Chown(foreign, 1, 1)
	if err := LockDataDir(foreign); err == nil || !strings.Contains(err.Error(), "owned by uid 1") {
		t.Errorf("foreign dir: %v", err)
	}
	if fi, _ := os.Stat(foreign); fi.Mode().Perm() != 0o755 {
		t.Error("refused dir was modified")
	}
}
```

`svc/firewall_test.go`:

```go
package svc

import (
	"slices"
	"strings"
	"testing"
)

func TestFirewallCommands(t *testing.T) {
	add := firewallAddCommands(17891, 17890)
	if len(add) != 2 {
		t.Fatalf("%d add commands", len(add))
	}
	for _, want := range [][]string{
		{"name=" + FirewallRuleLAN, "protocol=TCP", "localport=17891", "dir=in", "action=allow"},
		{"name=" + FirewallRuleDiscovery, "protocol=UDP", "localport=17890", "dir=in", "action=allow"},
	} {
		found := false
		for _, c := range add {
			if c[0] == "netsh" && slices.Contains(c, "add") && containsAll(c, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("no add command with %v in %v", want, add)
		}
	}
	for _, c := range firewallDeleteCommands() {
		if !slices.Contains(c, "delete") || !strings.HasPrefix(c[len(c)-1], "name=Sevjala-") {
			t.Errorf("delete command %v", c)
		}
	}
}

func containsAll(c, want []string) bool {
	for _, w := range want {
		if !slices.Contains(c, w) {
			return false
		}
	}
	return true
}
```

Append to `cmd/doctor_test.go` (and change `TestCheckConfig` to `if r, _ := checkConfig(dir); …` in both places):

```go
func TestCheckIdentityAndLAN(t *testing.T) {
	dir := t.TempDir()
	if r, id := checkIdentity(dir); r.OK || id != nil {
		t.Errorf("no identity passed: %+v", r)
	}
	id, _, err := identity.LoadOrCreate(dir)
	if err != nil {
		t.Fatal(err)
	}
	r, got := checkIdentity(dir)
	if !r.OK || got == nil || !strings.Contains(r.Detail, id.FP) {
		t.Errorf("identity: %+v", r)
	}

	srv, err := lanapi.Start(lanapi.Options{Addr: "127.0.0.1:0", DataDir: dir, Identity: id, Role: "node",
		Version: "test", Started: time.Now(), Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Shutdown(context.Background())
	cfg := config.Default(config.RoleNode)
	cfg.Listen = fmt.Sprintf("0.0.0.0:%d", srv.Port())
	if r := checkLAN(cfg, id); !r.OK {
		t.Errorf("own LAN API: %+v", r)
	}
	other, _, _ := identity.LoadOrCreate(t.TempDir())
	if r := checkLAN(cfg, other); r.OK || !strings.Contains(r.Detail, "not this agent") {
		t.Errorf("someone else's LAN API: %+v", r)
	}
	cfg.Listen = "127.0.0.1:1"
	if r := checkLAN(cfg, id); r.OK {
		t.Errorf("closed port passed: %+v", r)
	}
}
```

(imports: `context`, `fmt`, `log/slog`, `strings`, `time`, `github.com/jufianto/sevjala/identity`, `github.com/jufianto/sevjala/lanapi`.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./svc ./cmd`
Expected: FAIL to compile — `undefined: CheckDataDirLocked`, `undefined: firewallAddCommands`, `undefined: checkIdentity`.

- [ ] **Step 3: Implement**

`svc/lock_unix.go`:

```go
//go:build !windows

package svc

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// LockDataDir makes dir root-only. It refuses a dir someone else owns or a
// symlink, since either could carry a planted config (decisions.md D-010).
func LockDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("refusing %s: it is a symlink; use the real path", dir)
	}
	if uid := fi.Sys().(*syscall.Stat_t).Uid; uid != 0 {
		return fmt.Errorf("refusing %s: owned by uid %d, not root; remove it or 'chown -R root' it first", dir, uid)
	}
	if err := os.Chown(dir, 0, 0); err != nil {
		return err
	}
	return os.Chmod(dir, 0o700)
}

func CheckDataDirLocked(dir string) error {
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if uid := fi.Sys().(*syscall.Stat_t).Uid; uid != 0 {
		return fmt.Errorf("%s is owned by uid %d, not root", dir, uid)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Errorf("%s has mode %v; want 0700", dir, perm)
	}
	return nil
}
```

`svc/lock_windows.go`:

```go
package svc

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// lockedSDDL: owner Administrators; SYSTEM and Administrators full control,
// inherited by children; parent ACEs blocked.
const lockedSDDL = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// LockDataDir refuses a dir not owned by Administrators or SYSTEM, since a
// user could have pre-created it with a planted config (decisions.md D-010).
func LockDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !trusted(owner) {
		return fmt.Errorf("refusing %s: owned by %s, not Administrators or SYSTEM; remove it or take ownership as an administrator first", dir, account(owner))
	}
	want, err := windows.SecurityDescriptorFromString(lockedSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := want.DACL()
	if err != nil {
		return err
	}
	admins, _, err := want.Owner()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		admins, nil, dacl, nil)
}

func CheckDataDirLocked(dir string) error {
	sd, err := windows.GetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	if !trusted(owner) {
		return fmt.Errorf("%s is owned by %s", dir, account(owner))
	}
	ctrl, _, err := sd.Control()
	if err != nil {
		return err
	}
	if ctrl&windows.SE_DACL_PROTECTED == 0 {
		return fmt.Errorf("%s inherits permissions from its parent", dir)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	if dacl == nil {
		return fmt.Errorf("%s has no access list (everyone has access)", dir)
	}
	for i := range uint32(dacl.AceCount) {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, i, &ace); err != nil {
			return err
		}
		if sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart)); !trusted(sid) {
			return fmt.Errorf("%s grants access to %s", dir, account(sid))
		}
	}
	return nil
}

func trusted(s *windows.SID) bool {
	return s.IsWellKnown(windows.WinBuiltinAdministratorsSid) || s.IsWellKnown(windows.WinLocalSystemSid)
}

func account(s *windows.SID) string {
	acc, dom, _, err := s.LookupAccount("")
	if err != nil {
		return s.String()
	}
	return dom + `\` + acc
}
```

`svc/firewall.go`:

```go
package svc

import (
	"bytes"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

const (
	FirewallRuleLAN       = "Sevjala-LAN-API"
	FirewallRuleDiscovery = "Sevjala-Discovery"
)

func firewallAddCommands(tcpPort, udpPort int) [][]string {
	rule := func(name, proto string, port int) []string {
		return []string{"netsh", "advfirewall", "firewall", "add", "rule", "name=" + name, "dir=in", "action=allow",
			"protocol=" + proto, "localport=" + strconv.Itoa(port), "profile=any"}
	}
	return [][]string{rule(FirewallRuleLAN, "TCP", tcpPort), rule(FirewallRuleDiscovery, "UDP", udpPort)}
}

func firewallDeleteCommands() [][]string {
	var out [][]string
	for _, name := range []string{FirewallRuleLAN, FirewallRuleDiscovery} {
		out = append(out, []string{"netsh", "advfirewall", "firewall", "delete", "rule", "name=" + name})
	}
	return out
}

// AddFirewallRules (re)creates the inbound rules on Windows; elsewhere the OS
// firewall is the operator's to manage.
func AddFirewallRules(tcpPort, udpPort int) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	RemoveFirewallRules()
	for _, c := range firewallAddCommands(tcpPort, udpPort) {
		if out, err := exec.Command(c[0], c[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(c, " "), err, bytes.TrimSpace(out))
		}
	}
	return nil
}

// RemoveFirewallRules ignores errors: a missing rule is the goal.
func RemoveFirewallRules() {
	if runtime.GOOS != "windows" {
		return
	}
	for _, c := range firewallDeleteCommands() {
		_ = exec.Command(c[0], c[1:]...).Run()
	}
}

func FirewallRulesPresent() error {
	for _, name := range []string{FirewallRuleLAN, FirewallRuleDiscovery} {
		if err := exec.Command("netsh", "advfirewall", "firewall", "show", "rule", "name="+name).Run(); err != nil {
			return fmt.Errorf("rule %s missing; run 'sevjala service install' again", name)
		}
	}
	return nil
}
```

`cmd/service.go` install: after `checkNotInstalled` and before `PrepareDataDir` insert

```go
			if err := svc.LockDataDir(dataDir); err != nil {
				return fail(err)
			}
```

and after the group block (before `exe, _ := os.Executable()`):

```go
			cfg, err := config.Load(config.Path(dataDir))
			if err != nil {
				return fail(err)
			}
			if err := svc.AddFirewallRules(cfg.ListenPort(), cfg.DiscoveryPort); err != nil {
				return failf("firewall: %w", err)
			}
			if runtime.GOOS == "windows" {
				fmt.Fprintf(out, "opened the Windows firewall for %d/tcp and %d/udp\n", cfg.ListenPort(), cfg.DiscoveryPort)
			}
```

uninstall: after a successful `s.Uninstall()` add `svc.RemoveFirewallRules()`.

`cmd/doctor.go`: change `checkConfig` to return the config, add the new checks, and assemble them:

```go
func checkConfig(dataDir string) (checkResult, *config.Config) {
	cfg, err := config.Load(config.Path(dataDir))
	if err != nil {
		return checkResult{Name: "config", Detail: err.Error()}, nil
	}
	return checkResult{Name: "config", Detail: "role=" + cfg.Role, OK: true}, &cfg
}

func checkLockdown(dataDir string) checkResult {
	if err := svc.CheckDataDirLocked(dataDir); err != nil {
		return checkResult{Name: "lockdown", Detail: err.Error()}
	}
	return checkResult{Name: "lockdown", Detail: "admin/root only", OK: true}
}

func checkIdentity(dataDir string) (checkResult, *identity.Identity) {
	id, err := identity.Load(dataDir)
	if err != nil {
		return checkResult{Name: "identity", Detail: err.Error()}, nil
	}
	return checkResult{Name: "identity", Detail: shortID(id.AgentID) + "  " + id.FP, OK: true}, id
}

// checkLAN asks our own LAN API for /identity and expects our fingerprint.
func checkLAN(cfg config.Config, id *identity.Identity) checkResult {
	host, _, _ := net.SplitHostPort(cfg.Listen)
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, strconv.Itoa(cfg.ListenPort()))
	if id == nil {
		return checkResult{Name: "lan api", Detail: addr + ": no identity to compare with"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	info, err := lanapi.NewClient(id).Identity(ctx, addr)
	if err != nil {
		return checkResult{Name: "lan api", Detail: fmt.Sprintf("%s: %v", addr, err)}
	}
	if info.FP != id.FP {
		return checkResult{Name: "lan api", Detail: fmt.Sprintf("%s answers as %s, not this agent", addr, info.FP)}
	}
	return checkResult{Name: "lan api", Detail: addr + " → " + info.Role, OK: true}
}

func checkFirewall() checkResult {
	if err := svc.FirewallRulesPresent(); err != nil {
		return checkResult{Name: "firewall", Detail: err.Error()}
	}
	return checkResult{Name: "firewall", Detail: svc.FirewallRuleLAN + ", " + svc.FirewallRuleDiscovery, OK: true}
}
```

In `newDoctorCmd`'s `RunE`, replace the `rs := …` line with:

```go
			cfgCheck, cfg := checkConfig(dataDir)
			idCheck, id := checkIdentity(dataDir)
			rs := []checkResult{checkDataDir(dataDir), checkLockdown(dataDir), cfgCheck, idCheck}
			rs = append(rs, serviceChecks(v, endpoint)...)
			if cfg != nil {
				rs = append(rs, checkLAN(*cfg, id))
			}
			if runtime.GOOS == "windows" {
				rs = append(rs, checkFirewall())
			}
```

and update the `Short` to `"Check the data dir, config, identity, service, local and LAN APIs"`. Imports for `cmd/doctor.go`: `context`, `fmt`, `io`, `net`, `os`, `runtime`, `strconv`, `time`, cobra, `config`, `identity`, `lanapi`, `localapi`, `svc`.

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./svc ./cmd && GOOS=windows go vet ./... && GOOS=windows GOARCH=386 go build ./...`
Expected: PASS; Windows vet and 386 build clean.

- [ ] **Step 5: Commit** (two commits: lockdown/firewall, then CLI wiring)

```bash
git add svc/lock_unix.go svc/lock_windows.go svc/lock_unix_test.go svc/firewall.go svc/firewall_test.go
git commit -m "feat(svc): data dir lockdown (root 0700 / protected DACL) and Windows firewall rules"
git add cmd/service.go cmd/doctor.go cmd/doctor_test.go
git commit -m "feat(cmd): install locks the data dir and opens the firewall; doctor checks identity, LAN API, lockdown, firewall"
```

---

### Task 11: docs and full verification

**Files:**
- Modify: `docs/protocol.md`, `docs/decisions.md`, `docs/smoke-tests.md`, `docs/PROGRESS.md`, `README.md`

- [ ] **Step 1: `docs/decisions.md`** — add rows D-010…D-017 and tick the lockdown open question:

| ID | Decision |
|---|---|
| D-010 | Data dir lockdown moves into M1 (first private key): Unix root-owned 0700, Windows owner Administrators + protected DACL SYSTEM/Administrators; install refuses a dir it does not trust or a symlink. |
| D-011 | The pairing code lives only in the node agent's memory (no keyring in M1); an agent restart clears it. |
| D-012 | While a code is active a node accepts any client cert for `/identity` and `/pair` only; a successful pair replaces the trusted controller. No active code → unknown certs fail the handshake. |
| D-013 | LAN `GET /status` in M1 returns `agent_id, role, version, uptime_s, health, backups:[], problems:[]`; `store` arrives in M2. Local `/status` adds `agent_id`, `fp`. |
| D-014 | CLI `<agent>` = exact agent_id, else hostname (any case), else a unique agent_id prefix of ≥ 4 chars; ambiguity lists candidates. |
| D-015 | Discovery results are cached in controller memory; `pair` needs a prior `discover`/`agents add` in the same agent run. Discover refreshes hostname/address of a paired agent whose fp matches. |
| D-016 | Role logic lives in package `coord` behind `localapi.Backend` (not in the blueprint's package list; it keeps `localapi` free of LAN code). |
| D-017 | Windows firewall rules `Sevjala-LAN-API` (TCP) and `Sevjala-Discovery` (UDP) are port-scoped, inbound allow, all profiles, created by install and removed by uninstall. |

- [ ] **Step 2: `docs/protocol.md`** — move the M1 formats in: discovery datagrams (request text, reply JSON with field table), LAN API routes table with auth rules and error bodies, `identity/` files and `peers.json` v1, `agents` table schema (`user_version` 1), and the new local routes with request/response JSON. Mark the blueprint rows for discovery, pairing, LAN API and local routes as "implemented below".

- [ ] **Step 3: `docs/smoke-tests.md`** — add "M1 — discover, pair, ping across two PCs": install a controller on PC A and a node on PC B (same LAN), `sevjala discover` on A lists B, `sevjala code` on B, `sevjala pair <B> --code N` on A, `sevjala ping <B>` answers, `sevjala agents` shows B online; a third PC installed as a second controller gets "rejected this controller's certificate" on `ping` after `agents add` + a failed pair; Windows: `doctor` shows `firewall ok` and `lockdown ok`, `icacls C:\ProgramData\Sevjala` shows only SYSTEM and Administrators.

- [ ] **Step 4: `docs/PROGRESS.md`** — M1 row: `code done — two-PC smoke pending ([how to test](./smoke-tests.md))` with spec and plan links. **`README.md`** — a "Pair a node" snippet under "Try it".

- [ ] **Step 5: Full verification**

Run:
```bash
go vet ./... && GOOS=darwin go vet ./... && GOOS=windows go vet ./...
golangci-lint run
go test -race ./...
go mod tidy -diff
make build-all
docker run --rm -v "$PWD":/src -w /src golang:1.27.2 go test ./...
```
Expected: all clean; `dist/` has the six assets; the Docker run (root) executes `TestLockDataDirAsRoot` and passes.

- [ ] **Step 6: Commit**

```bash
git add docs README.md
git commit -m "docs: M1 protocol formats, decisions D-010..D-017, two-PC smoke steps"
```
