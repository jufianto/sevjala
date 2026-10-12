package identity

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

const peersFile = "peers.json"

// Peer is the controller a node trusts (one per node in v1).
type Peer struct {
	AgentID  string    `json:"agent_id"`
	FP       string    `json:"fp"`
	PairedAt time.Time `json:"paired_at"`
}

type peers struct {
	V          int   `json:"v"`
	Controller *Peer `json:"controller"`
}

// LoadController returns nil, nil when the node has never been paired.
func LoadController(dataDir string) (*Peer, error) {
	path := filepath.Join(Dir(dataDir), peersFile)
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p peers
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if p.V != 1 {
		return nil, fmt.Errorf("%s: unsupported version %d", path, p.V)
	}
	return p.Controller, nil
}

func SaveController(dataDir string, c Peer) error {
	raw, err := json.MarshalIndent(peers{V: 1, Controller: &c}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(Dir(dataDir), 0o700); err != nil {
		return err
	}
	return writeFile(filepath.Join(Dir(dataDir), peersFile), append(raw, '\n'), 0o600)
}
