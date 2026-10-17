package lanapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/inventory"
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
	if want := "this node's identity changed since pairing (reinstalled, or another PC now has its address): pair it again"; !errors.Is(err, ErrPinMismatch) || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// A stranger admitted during a pairing window must not get back in by resuming
// that TLS session after the code is gone.
func TestResumedSessionStillRejected(t *testing.T) {
	clk := &clock{t: time.Now()}
	id, dir := newID(t)
	codes := pairing.New(clk.now)
	s := start(t, "node", dir, id, codes)
	stranger := controller(t)
	cache := tls.NewLRUClientSessionCache(4)
	client := func() *http.Client {
		hc := stranger.http(id.FP, true, nil)
		hc.Transport.(*http.Transport).TLSClientConfig.ClientSessionCache = cache
		return hc
	}
	codes.Issue()
	var info IdentityInfo
	if err := stranger.do(context.Background(), client(), http.MethodGet, s.Addr(), "/identity", nil, &info); err != nil {
		t.Fatal(err)
	}
	clk.t = clk.t.Add(pairing.TTL + time.Minute)
	var st Status
	err := stranger.do(context.Background(), client(), http.MethodGet, s.Addr(), "/status", nil, &st)
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("after the code expired: %v, want a TLS rejection", err)
	}
}

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

// slowReader stands in for a source that stops moving; a Read that never
// returns would also block net/http's transport, which waits for its body writer.
type slowReader struct {
	r     io.Reader
	every time.Duration
	n     int
}

func (s *slowReader) Read(p []byte) (int, error) {
	if s.n++; s.n > 1 {
		time.Sleep(s.every)
	}
	return s.r.Read(p[:min(len(p), 1024)])
}

func TestPutBackupStallIsCancelled(t *testing.T) {
	n, st := startStoreNode(t)
	c := controller(t)
	c.Stall = 200 * time.Millisecond
	pair(t, c, n)
	m, data := backupData(t, 1<<20)
	start := time.Now()
	err := c.PutBackup(context.Background(), n.srv.Addr(), n.id.FP, m, &slowReader{r: bytes.NewReader(data), every: time.Second})
	if err == nil || !strings.Contains(err.Error(), "stalled") || time.Since(start) > 5*time.Second {
		t.Errorf("err = %v after %v", err, time.Since(start))
	}
	waitNoFiles(t, st)
}

func TestPutBackupWhileReceivingIs503(t *testing.T) {
	n, st := startStoreNode(t)
	c := controller(t)
	pair(t, c, n)
	m, data := backupData(t, 1<<20)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.PutBackup(ctx, n.srv.Addr(), n.id.FP, m, &blockingReader{first: data[:64<<10], ctx: ctx})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for len(storeFiles(t, st)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := c.PutBackup(context.Background(), n.srv.Addr(), n.id.FP, m, bytes.NewReader(data)); httpCode(err) != http.StatusServiceUnavailable {
		t.Errorf("second put while the first is in flight: %v", err)
	}
	cancel()
	<-done
}

// A node that takes every byte and then hangs (frozen process, dying disk in
// fsync) still ACKs at the TCP level; the worker must not wait forever.
func TestPutBackupNoAnswerAfterBodyTimesOut(t *testing.T) {
	id, _ := newID(t)
	hung := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	hung.TLS = &tls.Config{Certificates: []tls.Certificate{id.Cert}}
	hung.StartTLS()
	defer hung.Close()
	c := controller(t)
	c.Stall = 100 * time.Millisecond
	m, data := backupData(t, 64<<10)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	err := c.PutBackup(ctx, hung.Listener.Addr().String(), id.FP, m, bytes.NewReader(data))
	if err == nil || ctx.Err() != nil || time.Since(start) > 5*time.Second {
		t.Errorf("err = %v after %v", err, time.Since(start))
	}
}

func TestDeleteBackup(t *testing.T) {
	ctx := context.Background()
	n, st := startStoreNode(t)
	c := controller(t)
	pair(t, c, n)
	m, data := backupData(t, 1024)
	if err := c.PutBackup(ctx, n.srv.Addr(), n.id.FP, m, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DeleteBackup(ctx, n.srv.Addr(), n.id.FP, m.BackupID, " "); httpCode(err) != http.StatusBadRequest {
		t.Errorf("no reason: %v", err)
	}
	if _, err := c.DeleteBackup(ctx, n.srv.Addr(), n.id.FP, "nope", "retention"); httpCode(err) != http.StatusBadRequest {
		t.Errorf("bad id: %v", err)
	}
	if _, err := controller(t).DeleteBackup(ctx, n.srv.Addr(), n.id.FP, m.BackupID, "retention"); !errors.Is(err, ErrRejected) {
		t.Errorf("stranger: %v", err)
	}
	if deleted, err := c.DeleteBackup(ctx, n.srv.Addr(), n.id.FP, m.BackupID, "retention: test"); err != nil || !deleted {
		t.Fatalf("delete = %v, %v", deleted, err)
	}
	if f := storeFiles(t, st); len(f) != 0 {
		t.Errorf("files %v", f)
	}
	if deleted, err := c.DeleteBackup(ctx, n.srv.Addr(), n.id.FP, m.BackupID, "retention: test"); err != nil || deleted {
		t.Errorf("second delete = %v, %v", deleted, err)
	}
}

func TestControllerRefusesDelete(t *testing.T) {
	id, dir := newID(t)
	s := start(t, "controller", dir, id, nil)
	c := controller(t)
	var out DeleteResponse
	err := c.do(context.Background(), c.http(id.FP, false, nil), http.MethodDelete, s.Addr(), "/backups/BK-20261013-101500-A8F2",
		DeleteRequest{Reason: "x"}, &out)
	if httpCode(err) != http.StatusForbidden {
		t.Errorf("err = %v", err)
	}
}

func startNodeWithStore(t *testing.T, minFree int64) (agent, *inventory.Store) {
	t.Helper()
	id, dir := newID(t)
	st, err := inventory.Open(filepath.Join(dir, "store"), minFree, discard)
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

func TestStatusReportsLowSpace(t *testing.T) {
	n, _ := startNodeWithStore(t, 1<<62)
	c := controller(t)
	pair(t, c, n)
	s, err := c.Status(context.Background(), n.srv.Addr(), n.id.FP)
	if err != nil {
		t.Fatal(err)
	}
	if s.Store == nil || s.Store.MinFreeBytes != 1<<62 || s.Health != "warning" || len(s.Problems) != 1 ||
		!strings.HasPrefix(s.Problems[0], "only ") || !strings.HasSuffix(s.Problems[0], " free, below the 4.0 EiB this PC keeps free") {
		t.Errorf("status %+v", s)
	}
}

// An unreadable store is an unknown inventory, not an empty one: the
// controller must not conclude every copy vanished.
func TestStatusUnreadableStoreHasNoInventory(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a non-root Unix user to make the store unreadable")
	}
	n, st := startNodeWithStore(t, 0)
	c := controller(t)
	pair(t, c, n)
	if err := os.Chmod(st.Dir(), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(st.Dir(), 0o700) })
	s, err := c.Status(context.Background(), n.srv.Addr(), n.id.FP)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backups != nil || s.Health != "warning" {
		t.Errorf("status %+v", s)
	}
}

func TestPinMismatchIsTyped(t *testing.T) {
	n := startNode(t)
	c := controller(t)
	pair(t, c, n)
	if _, err := c.Status(context.Background(), n.srv.Addr(), "sha256:replaced"); !errors.Is(err, ErrPinMismatch) {
		t.Errorf("err = %v", err)
	}
}

func TestControllerContactIsRecorded(t *testing.T) {
	n := startNode(t)
	if n.srv.Paired() || !n.srv.ControllerContact().IsZero() {
		t.Fatalf("before pairing: paired %v, contact %v", n.srv.Paired(), n.srv.ControllerContact())
	}
	c := controller(t)
	pair(t, c, n)
	if !n.srv.Paired() {
		t.Error("not paired after pairing")
	}
	before := time.Now()
	if _, err := c.Status(context.Background(), n.srv.Addr(), n.id.FP); err != nil {
		t.Fatal(err)
	}
	if got := n.srv.ControllerContact(); got.Before(before) {
		t.Errorf("contact %v, want after %v", got, before)
	}
	if _, err := controller(t).Identity(context.Background(), n.srv.Addr()); err != nil {
		t.Fatal(err)
	}
	if got := n.srv.ControllerContact(); got.After(time.Now()) {
		t.Errorf("contact %v", got)
	}
}
