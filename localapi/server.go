package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"
)

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

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type Server struct {
	srv *http.Server
	ln  net.Listener
}

func Start(endpoint string, h http.Handler, log *slog.Logger) (*Server, error) {
	ln, warn, err := listen(endpoint)
	if err != nil {
		return nil, fmt.Errorf("local api %s: %w", endpoint, err)
	}
	if warn != "" {
		log.Warn(warn)
	}
	s := &Server{srv: &http.Server{Handler: h, ReadHeaderTimeout: 5 * time.Second}, ln: ln}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("local api stopped", "err", err)
		}
	}()
	return s, nil
}

// Shutdown closes the listener itself, removing a Unix socket file, because
// Serve may not have started yet and would only close it later.
func (s *Server) Shutdown(ctx context.Context) error {
	err := s.srv.Shutdown(ctx)
	_ = s.ln.Close()
	return err
}
