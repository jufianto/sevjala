package manage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/jufianto/sevjala/logx"
)

// HeaderBy carries the account that asked on the manager, for the
// controller's log.
const HeaderBy = "X-Sevjala-By"

var errNoLink = errors.New("no manager link in time")

// RelayState is what the node knows about its controller when a request comes.
type RelayState struct {
	Role        string // this agent's role
	Paired      bool
	Known       bool // a poll has said whether this PC is a manager
	Manager     bool
	Host        string // the controller's hostname, from the polls
	Self        string // this PC's hostname
	LastContact time.Time
}

// Relay serves /controller/<route> on the node's local API by sending
// <route> to the controller over a manager link.
func Relay(p *Pool, state func() RelayState, wait time.Duration) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport: p.transport(wait),
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = "http", "controller"
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/controller")
			pr.Out.URL.RawPath = ""
			pr.Out.Header.Set(HeaderBy, logx.By(pr.In.Context()))
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			st := state()
			switch {
			case !errors.Is(err, errNoLink):
				fail(w, http.StatusBadGateway, "the link to %s broke mid-way; try again", st.Host)
			case !st.Known:
				fail(w, http.StatusServiceUnavailable, "the controller has not contacted this PC since Sevjala started here (is it on and on the network?)")
			case !st.Manager:
				notManager(w, st)
			default:
				fail(w, http.StatusServiceUnavailable, "%s has not connected to this PC for %s (is it on and on the network?)",
					st.Host, ago(time.Since(st.LastContact)))
			}
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := state()
		switch {
		case st.Role != "node":
			fail(w, http.StatusConflict, "this PC is the controller; run the command without --controller")
		case !st.Paired:
			fail(w, http.StatusConflict, "this PC is not paired with a controller")
		case st.Known && !st.Manager:
			notManager(w, st)
		default:
			proxy.ServeHTTP(w, r)
		}
	})
}

func notManager(w http.ResponseWriter, st RelayState) {
	fail(w, http.StatusForbidden, "this PC is not a manager of %s (on %s, an administrator runs: sevjala agents manager %s on)",
		st.Host, st.Host, st.Self)
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%d s", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h", int(d.Hours()))
}

func fail(w http.ResponseWriter, code int, format string, a ...any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf(format, a...)})
}
