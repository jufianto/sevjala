package localapi

import (
	"context"
	"fmt"
	"time"
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
	AgentID  string    `json:"agent_id"`
	Hostname string    `json:"hostname"`
	Addr     string    `json:"addr"`
	Role     string    `json:"role"`
	Version  string    `json:"version"`
	FP       string    `json:"fp"`
	PairedAt time.Time `json:"paired_at"`
	LastSeen time.Time `json:"last_seen,omitzero"`
	State    string    `json:"state"`
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
