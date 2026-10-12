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
