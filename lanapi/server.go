package lanapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/logx"
	"github.com/jufianto/sevjala/pairing"
)

const (
	maxBody  = 4 << 10
	bodyIdle = time.Minute
)

type Options struct {
	Addr     string
	DataDir  string
	Identity *identity.Identity
	Role     string
	Version  string
	Hostname string
	Started  time.Time
	Codes    *pairing.Codes   // nil on a controller
	Store    *inventory.Store // nil on a controller
	Log      *slog.Logger
}

type Server struct {
	opt Options
	srv *http.Server
	ln  net.Listener

	mu         sync.Mutex
	controller *identity.Peer
	contact    time.Time // last authenticated request from the controller, guarded by mu
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
		// A resumed session skips VerifyPeerCertificate, which is the pairing gate.
		SessionTicketsDisabled: true,
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
		IdleTimeout:       time.Minute,
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
	mux.HandleFunc("GET /status", s.pairedOnly(s.status))
	mux.HandleFunc("GET /backups/{id}", s.pairedOnly(s.getBackup))
	mux.HandleFunc("PUT /backups/{id}", s.pairedOnly(s.putBackup))
	mux.HandleFunc("DELETE /backups/{id}", s.pairedOnly(s.deleteBackup))
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

// pairedOnly guards every route but /identity and /pair: a connection admitted
// during a pairing window still carries a stranger's certificate.
func (s *Server) pairedOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if fp := peerFP(r); fp == "" || fp != s.controllerFP() {
			writeError(w, http.StatusForbidden, "not paired with this controller")
			return
		}
		s.mu.Lock()
		s.contact = time.Now()
		s.mu.Unlock()
		h(w, r)
	}
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) {
	st := Status{AgentID: s.opt.Identity.AgentID, Role: s.opt.Role, Version: s.opt.Version,
		UptimeS: int64(time.Since(s.opt.Started).Seconds()), Health: "healthy", Backups: []BackupRef{}, Problems: []string{}}
	if s.opt.Store != nil {
		st.Store = &StoreInfo{Path: s.opt.Store.Dir(), MinFreeBytes: s.opt.Store.MinFree()}
		if inv, err := s.opt.Store.Scan(); err != nil {
			st.Backups = nil
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
			if free < st.Store.MinFreeBytes {
				st.Problems = append(st.Problems, fmt.Sprintf("only %s free, below the %s this PC keeps free", inventory.HumanBytes(free), inventory.HumanBytes(st.Store.MinFreeBytes)))
			}
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
	m, sha, size, err := backupHeaders(r.Header, id)
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

// getBackup streams a stored backup to the controller (restore). The store holds
// it until the stream ends, so a DELETE of it waits.
func (s *Server) getBackup(w http.ResponseWriter, r *http.Request) {
	if s.opt.Store == nil {
		writeError(w, http.StatusConflict, "this agent is a controller; backups live on nodes")
		return
	}
	id := r.PathValue("id")
	m, rc, err := s.opt.Store.Open(id)
	if err != nil {
		code := receiveStatus(err)
		if errors.Is(err, fs.ErrNotExist) {
			code = http.StatusNotFound
		}
		writeError(w, code, err.Error())
		return
	}
	defer rc.Close()
	raw, err := json.Marshal(m)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h := w.Header()
	h.Set("Content-Type", "application/gzip")
	h.Set("Content-Length", strconv.FormatInt(m.Size, 10))
	h.Set(HeaderSHA256, m.SHA256)
	h.Set(HeaderSize, strconv.FormatInt(m.Size, 10))
	h.Set(HeaderManifest, base64.StdEncoding.EncodeToString(raw))
	w.WriteHeader(http.StatusOK)
	n, err := io.Copy(&deadlineWriter{w: w, rc: http.NewResponseController(w)}, rc)
	s.opt.Log.Info("backup served", logx.Event(logx.EventBackupServed), "backup_id", id, "bytes", n, "complete", err == nil && n == m.Size,
		"remote", r.RemoteAddr)
}

// deleteBackup is retention's only way to remove a backup (blueprint AD-08);
// the reason is logged on the node.
func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	if s.opt.Store == nil {
		writeError(w, http.StatusConflict, "this agent is a controller; backups live on nodes")
		return
	}
	var req DeleteRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		writeError(w, http.StatusBadRequest, `a delete needs a body {"reason":"…"}`)
		return
	}
	id := r.PathValue("id")
	deleted, err := s.opt.Store.Delete(id)
	if err != nil {
		writeError(w, receiveStatus(err), err.Error())
		return
	}
	s.opt.Log.Info("backup deleted", logx.Event(logx.EventBackupDeleted), "backup_id", id, "reason", req.Reason,
		"deleted", deleted, "remote", r.RemoteAddr)
	writeJSON(w, http.StatusOK, DeleteResponse{BackupID: id, Deleted: deleted})
}

func backupHeaders(h http.Header, id string) (inventory.Manifest, string, int64, error) {
	var m inventory.Manifest
	size, err := strconv.ParseInt(h.Get(HeaderSize), 10, 64)
	if err != nil {
		return m, "", 0, fmt.Errorf("%w: %s: %w", inventory.ErrInvalid, HeaderSize, err)
	}
	raw := h.Get(HeaderManifest)
	if len(raw) > 8<<10 {
		return m, "", 0, fmt.Errorf("%w: %s over 8 KiB", inventory.ErrInvalid, HeaderManifest)
	}
	js, err := base64.StdEncoding.DecodeString(raw)
	if err == nil {
		err = json.Unmarshal(js, &m)
	}
	if err != nil {
		return m, "", 0, fmt.Errorf("%w: %s: %w", inventory.ErrInvalid, HeaderManifest, err)
	}
	if m.BackupID != id {
		return m, "", 0, fmt.Errorf("%w: manifest is for %q, not %q", inventory.ErrInvalid, m.BackupID, id)
	}
	return m, h.Get(HeaderSHA256), size, nil
}

func receiveStatus(err error) int {
	switch {
	case errors.Is(err, inventory.ErrInvalid):
		return http.StatusBadRequest
	case errors.Is(err, inventory.ErrConflict):
		return http.StatusConflict
	case errors.Is(err, inventory.ErrBusy):
		return http.StatusServiceUnavailable
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

// deadlineWriter frees the slot when the controller stops reading.
type deadlineWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	_ = d.rc.SetWriteDeadline(time.Now().Add(bodyIdle))
	return d.w.Write(p)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) Paired() bool { return s.controllerFP() != "" }

// ControllerContact is when the paired controller last called; zero until it
// calls after this start.
func (s *Server) ControllerContact() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.contact
}
