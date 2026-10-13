package localapi

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/jufianto/sevjala/inventory"
)

type Status struct {
	Version string     `json:"version"`
	Role    string     `json:"role"`
	PID     int        `json:"pid"`
	UptimeS int64      `json:"uptime_s"`
	DataDir string     `json:"data_dir"`
	AgentID string     `json:"agent_id"`
	FP      string     `json:"fp"`
	Store   *StoreInfo `json:"store,omitempty"`
}

type Code struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
	AgentID   string    `json:"agent_id"`
	FP        string    `json:"fp"`
}

type Discovered struct {
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname"`
	Addr     string `json:"addr"`
	Role     string `json:"role"`
	Version  string `json:"version"`
	OS       string `json:"os,omitempty"`
	FP       string `json:"fp"`
	Paired   bool   `json:"paired"`
}

type Agent struct {
	AgentID   string    `json:"agent_id"`
	Hostname  string    `json:"hostname"`
	Addr      string    `json:"addr"`
	Role      string    `json:"role"`
	Version   string    `json:"version"`
	FP        string    `json:"fp"`
	PairedAt  time.Time `json:"paired_at"`
	LastSeen  time.Time `json:"last_seen,omitzero"`
	State     string    `json:"state"`
	FreeBytes int64     `json:"free_bytes"`
	Problem   string    `json:"problem,omitempty"` // why the last poll warned or failed
}

type Ping struct {
	Agent   Agent  `json:"agent"`
	RTTMs   int64  `json:"rtt_ms"`
	UptimeS int64  `json:"uptime_s"`
	Health  string `json:"health"`
}

// Backend is what the agent serves over the pipe; a call for the other role
// returns a 409 *Error.
type Backend interface {
	Status() Status
	Code() (Code, error)
	Discover(ctx context.Context) ([]Discovered, error)
	AddAgent(ctx context.Context, addr string) (Discovered, error)
	Pair(ctx context.Context, agent, code, fp string) (Agent, error)
	Agents(ctx context.Context) ([]Agent, error)
	Ping(ctx context.Context, agent string) (Ping, error)
	RemoveAgent(ctx context.Context, agent string, force bool) (Agent, error)
	DetectDatabase(ctx context.Context) (Detection, error)
	SetDatabase(ctx context.Context, s DBSettings) (Detection, error)
	BackupNow(ctx context.Context, db string, tags []string) ([]BackupJob, error)
	Backups(ctx context.Context, all bool) ([]Backup, error)
	Backup(ctx context.Context, id string) (Backup, error)
	RunRetention(ctx context.Context, dryRun bool) (RetentionPlan, error)
	Policies(ctx context.Context) ([]Policy, error)
	AddPolicy(ctx context.Context, p PolicyChange) (Policy, error)
	SetPolicy(ctx context.Context, name string, p PolicyChange) (Policy, error)
	RemovePolicy(ctx context.Context, name string) error
	// Held and OpenHeld read this PC's own backups: a node's store or the
	// controller's staging.
	Held(ctx context.Context) ([]inventory.Manifest, error)
	OpenHeld(ctx context.Context, id string) (inventory.Manifest, io.ReadCloser, error)
}

// Error carries an HTTP status across the pipe; Msg is shown to the operator.
type Error struct {
	Code int
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Errorf formats like fmt.Sprintf: use %v for errors, there is no wrapping.
func Errorf(code int, format string, a ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

type list[T any] struct {
	Agents []T `json:"agents"`
}

func listOf[T any](xs []T) list[T] {
	if xs == nil {
		xs = []T{}
	}
	return list[T]{Agents: xs}
}

const (
	BackupDumping = "dumping"
	BackupFailed  = "failed"
)

type DBDatabase struct {
	Name        string   `json:"name"`
	Tables      int      `json:"tables"`
	Consistency string   `json:"consistency"`
	NonInnoDB   []string `json:"non_innodb"`
	Configured  bool     `json:"configured"`
}

type Detection struct {
	Dumper        string       `json:"dumper"`
	Client        string       `json:"client"`
	ServerVersion string       `json:"server_version"`
	Flavour       string       `json:"flavour"`
	Databases     []DBDatabase `json:"databases"`
}

// DBSettings is a partial update: nil fields keep the agent's value; a nil
// Databases keeps the list, an empty one clears it.
type DBSettings struct {
	Host      *string  `json:"host,omitempty"`
	Port      *int     `json:"port,omitempty"`
	User      *string  `json:"user,omitempty"`
	Password  *string  `json:"password,omitempty"`
	Dumper    *string  `json:"dumper,omitempty"`
	Databases []string `json:"databases"`
}

type BackupJob struct {
	BackupID string `json:"backup_id"`
	Database string `json:"database"`
}

type BackupCopy struct {
	AgentID    string    `json:"agent_id"`
	Hostname   string    `json:"hostname"`
	State      string    `json:"state"`
	Attempts   int       `json:"attempts"`
	LastError  string    `json:"last_error,omitempty"`
	VerifiedAt time.Time `json:"verified_at,omitzero"`
}

// Backup is a stored backup, or one still dumping (State BackupDumping,
// Bytes so far) or whose dump failed (State BackupFailed, Error).
type Backup struct {
	BackupID  string       `json:"backup_id"`
	Database  string       `json:"database"`
	CreatedAt time.Time    `json:"created_at,omitzero"`
	Size      int64        `json:"size"`
	SHA256    string       `json:"sha256"`
	Tags      []string     `json:"tags"`
	State     string       `json:"state"`
	Error     string       `json:"error,omitempty"`
	Bytes     int64        `json:"bytes,omitempty"`
	Copies    []BackupCopy `json:"copies"`
}

type StoreInfo struct {
	Path      string   `json:"path"`
	FreeBytes int64    `json:"free_bytes"`
	Backups   int      `json:"backups"`
	Problems  []string `json:"problems"`
}

type backupJobs struct {
	Backups []BackupJob `json:"backups"`
}

type backupList struct {
	Backups []Backup `json:"backups"`
}

type heldList struct {
	Backups []inventory.Manifest `json:"backups"`
}

type PolicyTarget struct {
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname"`
}

// Policy is a schedule and how many backups with its tag each node keeps; To
// is empty for every paired node, NextRun zero when it never fires.
type Policy struct {
	Name     string         `json:"name"`
	Schedule string         `json:"schedule"`
	Keep     int            `json:"keep"`
	To       []PolicyTarget `json:"to"`
	Enabled  bool           `json:"enabled"`
	NextRun  time.Time      `json:"next_run,omitzero"`
}

// PolicyChange adds (Name, Keep required) or changes a policy: nil fields keep
// the current value. To holds agent references; [] or ["all"] is every node.
type PolicyChange struct {
	Name     string   `json:"name,omitempty"`
	Schedule *string  `json:"schedule,omitempty"`
	Keep     *int     `json:"keep,omitempty"`
	To       []string `json:"to"`
	Enabled  *bool    `json:"enabled,omitempty"`
}

type policyList struct {
	Policies []Policy `json:"policies"`
}

// RetentionAction is one line of a retention plan; BackupID is empty when the
// line is about a whole node (skipped while offline).
type RetentionAction struct {
	BackupID string `json:"backup_id,omitempty"`
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname"`
	Reason   string `json:"reason"`
}

type RetentionPlan struct {
	DryRun   bool              `json:"dry_run"`
	Deletes  []RetentionAction `json:"deletes"`
	Sends    []RetentionAction `json:"sends"`
	Kept     []RetentionAction `json:"kept"`
	Unstaged []string          `json:"unstaged"`
	Expired  []string          `json:"expired"`
}
