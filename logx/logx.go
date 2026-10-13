package logx

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	MaxBytes = 10 << 20
	Keep     = 10
	FileName = "sevjala.log"
)

// Event names are provisional until the PRD's event list (section 25) is in the repo.
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
	EventPartRemoved        = "part_removed"
)

func Event(name string) slog.Attr { return slog.String("event", name) }

// Open logs JSON lines to dir/sevjala.log; foreground also prints text to stderr.
func Open(dir string, foreground bool) (*slog.Logger, io.Closer, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, err
	}
	w, err := NewRotatingWriter(filepath.Join(dir, FileName), MaxBytes, Keep)
	if err != nil {
		return nil, nil, err
	}
	var h slog.Handler = slog.NewJSONHandler(w, nil)
	if foreground {
		h = slog.NewMultiHandler(h, slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(h), w, nil
}
