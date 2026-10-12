// Package lanapi is the agent's HTTPS API on the LAN: mutual TLS with pinned
// fingerprints (blueprint: Coordination → LAN API).
package lanapi

type IdentityInfo struct {
	AgentID  string `json:"agent_id"`
	Hostname string `json:"hostname"`
	Role     string `json:"role"`
	Version  string `json:"version"`
	FP       string `json:"fp"`
}

type PairRequest struct {
	ControllerID string `json:"controller_id"`
	ControllerFP string `json:"controller_fp"`
	Proof        string `json:"proof"`
}

type PairResponse struct {
	AgentID string `json:"agent_id"`
	FP      string `json:"fp"`
}

type BackupRef struct {
	BackupID string `json:"backup_id"`
	Size     int64  `json:"size"`
	SHA256   string `json:"sha256"`
	State    string `json:"state"`
}

// Status is the M1 subset of the blueprint's GET /status; M2 adds the store.
type Status struct {
	AgentID  string      `json:"agent_id"`
	Role     string      `json:"role"`
	Version  string      `json:"version"`
	UptimeS  int64       `json:"uptime_s"`
	Health   string      `json:"health"`
	Backups  []BackupRef `json:"backups"`
	Problems []string    `json:"problems"`
}
