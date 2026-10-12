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
