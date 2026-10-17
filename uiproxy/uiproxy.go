// Package uiproxy serves the dashboard for 'sevjala ui' on 127.0.0.1 (AD-14):
// the embedded Svelte build, /api passed to the local API as the user who
// started it, and /events, the agent's state as it changes.
package uiproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jufianto/sevjala/config"
	"github.com/jufianto/sevjala/localapi"
)

const (
	// changeHeader marks requests sent by the dashboard's own script: a page
	// on another site cannot add it without a CORS preflight, which fails.
	changeHeader = "X-Sevjala-UI"
	pingEvery    = 15 * time.Second
	csp          = "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; " +
		"frame-ancestors 'none'; base-uri 'none'; form-action 'none'"
)

// State is what /events sends: the local status, and the backup PCs on a
// controller.
type State struct {
	Status localapi.Status  `json:"status"`
	Agents []localapi.Agent `json:"agents,omitempty"`
}

type Server struct {
	Poll time.Duration // how often /events asks the agent

	assets fs.FS
	ln     net.Listener
	host   string
	link   string
	cookie string // named per port: browsers keep cookies per host, not per port
	cl     *localapi.Client
	proxy  *httputil.ReverseProxy

	mu        sync.Mutex
	token     string // the launch link's; empty once used
	session   string
	clients   int
	idleSince time.Time
}

// New prepares a dashboard on ln for the agent at endpoint. Its one-time link
// is URL().
func New(endpoint string, assets fs.FS, ln net.Listener) (*Server, error) {
	token, err := random()
	if err != nil {
		return nil, err
	}
	session, err := random()
	if err != nil {
		return nil, err
	}
	s := &Server{Poll: 2 * time.Second, assets: assets, ln: ln, host: ln.Addr().String(),
		cl: localapi.NewClient(endpoint), token: token, session: session}
	s.link = s.Base() + "/?token=" + token
	_, port, _ := net.SplitHostPort(s.host)
	s.cookie = "sevjala_session_" + port
	s.proxy = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host, r.Out.Host = "http", "sevjala", "sevjala"
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, "/api")
			r.Out.URL.RawPath = strings.TrimPrefix(r.In.URL.RawPath, "/api")
			r.Out.Header.Del("Cookie")
			r.Out.Header.Del(changeHeader)
		},
		Transport:     localapi.NewTransport(endpoint),
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": down(err)})
		},
	}
	return s, nil
}

func random() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func (s *Server) Base() string { return "http://" + s.host }

// URL opens a session once; every later visit needs that session's cookie.
func (s *Server) URL() string { return s.link }

// Serve answers until ctx ends or no dashboard tab has been open for idle.
func (s *Server) Serve(ctx context.Context, idle time.Duration) error {
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 5 * time.Second}
	s.mu.Lock()
	s.idleSince = time.Now()
	s.mu.Unlock()
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		tick := time.NewTicker(min(idle/4, time.Second))
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
			case <-tick.C:
				if !s.idleFor(idle) {
					continue
				}
			}
			sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = srv.Shutdown(sctx)
			cancel()
			return
		}
	}()
	if err := srv.Serve(s.ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) idleFor(d time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clients == 0 && time.Since(s.idleSince) > d
}

func (s *Server) track(d int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients += d
	if s.clients == 0 {
		s.idleSince = time.Now()
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	// Another name for this address (DNS rebinding) would let a web page read it.
	if r.Host != s.host {
		page(w, http.StatusForbidden, "This dashboard answers only at "+s.Base()+".")
		return
	}
	if r.URL.Path == "/" && r.URL.Query().Has("token") {
		s.open(w, r, r.URL.Query().Get("token"))
		return
	}
	api := r.URL.Path == "/events" || strings.HasPrefix(r.URL.Path, "/api/")
	if !s.hasSession(r) {
		const msg = "Open the dashboard with: sevjala ui"
		if api {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": msg})
		} else {
			page(w, http.StatusUnauthorized, msg)
		}
		return
	}
	switch {
	case r.URL.Path == "/events":
		s.events(w, r)
	case api:
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(changeHeader) != "1" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "changes are accepted only from the dashboard page"})
			return
		}
		s.proxy.ServeHTTP(w, r)
	default:
		s.static(w, r)
	}
}

func (s *Server) open(w http.ResponseWriter, r *http.Request, token string) {
	s.mu.Lock()
	ok := s.token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) == 1
	if ok {
		s.token = ""
	}
	s.mu.Unlock()
	if !ok {
		page(w, http.StatusForbidden, "This link was already used or is not valid. Open the dashboard again with: sevjala ui")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: s.cookie, Value: s.session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) hasSession(r *http.Request) bool {
	c, err := r.Cookie(s.cookie)
	return err == nil && subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.session)) == 1
}

func (s *Server) static(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" {
		name = "index.html"
		if _, err := fs.Stat(s.assets, name); err != nil {
			page(w, http.StatusNotFound, "This sevjala was built without the dashboard (build it with: make ui). Every command still works.")
			return
		}
	}
	if fi, err := fs.Stat(s.assets, name); err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	http.ServeFileFS(w, r, s.assets, name)
}

// events sends the agent's state whenever it changes, and a comment now and
// then so a quiet connection is not taken for a dead one.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	s.track(1)
	defer s.track(-1)
	tick := time.NewTicker(s.Poll)
	defer tick.Stop()
	var last []byte
	sent := time.Now()
	for {
		msg := s.state(r.Context())
		switch {
		case !bytes.Equal(msg, last):
			_, _ = w.Write(msg)
			last, sent = msg, time.Now()
			fl.Flush()
		case time.Since(sent) > pingEvery:
			_, _ = io.WriteString(w, ": ping\n\n")
			sent = time.Now()
			fl.Flush()
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) state(ctx context.Context) []byte {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	st, err := s.cl.Status(ctx)
	if err != nil {
		b, _ := json.Marshal(map[string]string{"error": down(err)})
		return fmt.Appendf(nil, "event: down\ndata: %s\n\n", b)
	}
	v := State{Status: st}
	if st.Role == config.RoleController {
		v.Agents, _ = s.cl.Agents(ctx)
	}
	b, _ := json.Marshal(v)
	return fmt.Appendf(nil, "event: state\ndata: %s\n\n", b)
}

// down words a failed call to the agent the way the tray does.
func down(err error) string {
	var le *localapi.Error
	switch {
	case errors.As(err, &le):
		return le.Msg
	case errors.Is(err, fs.ErrPermission):
		return "This account may not use Sevjala"
	}
	return "The Sevjala service is not running"
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func page(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>Sevjala</title>`+
		`<p style="font:16px system-ui,sans-serif;margin:3em auto;max-width:36em">%s</p>`, html.EscapeString(msg))
}
