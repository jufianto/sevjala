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
	EventAgentStarted           = "agent_started"
	EventAgentStopped           = "agent_stopped"
	EventServiceInstalled       = "service_installed"
	EventServiceUninstalled     = "service_uninstalled"
	EventIdentityCreated        = "identity_created"
	EventDiscoveryRun           = "discovery_run"
	EventAgentAdded             = "agent_added"
	EventAgentRemoved           = "agent_removed"
	EventPairCodeIssued         = "pair_code_issued"
	EventPairSucceeded          = "pair_succeeded"
	EventPairRejected           = "pair_rejected"
	EventPeerRejected           = "peer_rejected"
	EventPartRemoved            = "part_removed"
	EventBackupReceived         = "backup_received"
	EventBackupRejected         = "backup_rejected"
	EventBackupStarted          = "backup_started"
	EventBackupCreated          = "backup_created"
	EventBackupFailed           = "backup_failed"
	EventTransferStarted        = "transfer_started"
	EventTransferVerified       = "transfer_verified"
	EventTransferFailed         = "transfer_failed"
	EventDBConfigured           = "db_configured"
	EventKeyringFallback        = "keyring_fallback"
	EventBackupDeleted          = "backup_deleted"
	EventBackupServed           = "backup_served"
	EventPolicyChanged          = "policy_changed"
	EventScheduleFired          = "schedule_fired"
	EventAgentOnline            = "agent_online"
	EventAgentWarning           = "agent_warning"
	EventAgentOffline           = "agent_offline"
	EventAgentError             = "agent_error"
	EventRetentionDelete        = "retention_delete"
	EventBackfillQueued         = "backfill_queued"
	EventStagingPruned          = "staging_pruned"
	EventUnknownBackup          = "unknown_backup"
	EventRestoreStarted         = "restore_started"
	EventRestoreSourceFail      = "restore_source_failed"
	EventCopyCorrupt            = "copy_corrupt"
	EventRestoreSucceeded       = "restore_succeeded"
	EventRestoreFailed          = "restore_failed"
	EventBackupExported         = "backup_exported"
	EventManagerSet             = "manager_set"
	EventManagerLinkFailed      = "manager_link_failed"
	EventAgentRenamed           = "agent_renamed"
	EventControllerDisconnected = "controller_disconnected"
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
	l := JSON(w)
	if foreground {
		l = slog.New(slog.NewMultiHandler(l.Handler(), slog.NewTextHandler(os.Stderr, nil)))
	}
	return l, w, nil
}
