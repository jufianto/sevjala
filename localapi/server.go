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
			FP    string `json:"fp"`
		}
		if decode(w, r, &req) {
			a, err := b.Pair(r.Context(), req.Agent, req.Code, req.FP)
			reply(w, a, err)
		}
	})
	mux.HandleFunc("POST /agents/{agent}/ping", func(w http.ResponseWriter, r *http.Request) {
		p, err := b.Ping(r.Context(), r.PathValue("agent"))
		reply(w, p, err)
	})
	mux.HandleFunc("DELETE /agents/{agent}", func(w http.ResponseWriter, r *http.Request) {
		a, err := b.RemoveAgent(r.Context(), r.PathValue("agent"), r.URL.Query().Get("force") == "true")
		reply(w, a, err)
	})
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
		bs, err := b.Backups(r.Context(), r.URL.Query().Get("all") == "1")
		if bs == nil {
			bs = []Backup{}
		}
		reply(w, backupList{Backups: bs}, err)
	})
	mux.HandleFunc("GET /backups/{id}", func(w http.ResponseWriter, r *http.Request) {
		bk, err := b.Backup(r.Context(), r.PathValue("id"))
		reply(w, bk, err)
	})
	mux.HandleFunc("POST /retention/run", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			DryRun bool `json:"dry_run"`
		}
		if decode(w, r, &req) {
			p, err := b.RunRetention(r.Context(), req.DryRun)
			reply(w, p, err)
		}
	})
	mux.HandleFunc("GET /policies", func(w http.ResponseWriter, r *http.Request) {
		ps, err := b.Policies(r.Context())
		if ps == nil {
			ps = []Policy{}
		}
		reply(w, policyList{Policies: ps}, err)
	})
	mux.HandleFunc("POST /policies", func(w http.ResponseWriter, r *http.Request) {
		var ch PolicyChange
		if decode(w, r, &ch) {
			p, err := b.AddPolicy(r.Context(), ch)
			reply(w, p, err)
		}
	})
	mux.HandleFunc("PATCH /policies/{name}", func(w http.ResponseWriter, r *http.Request) {
		var ch PolicyChange
		if decode(w, r, &ch) {
			p, err := b.SetPolicy(r.Context(), r.PathValue("name"), ch)
			reply(w, p, err)
		}
	})
	mux.HandleFunc("DELETE /policies/{name}", func(w http.ResponseWriter, r *http.Request) {
		reply(w, struct{}{}, b.RemovePolicy(r.Context(), r.PathValue("name")))
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
