package logx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenWritesJSONEvents(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	log, closer, err := Open(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	log.Info("agent started", Event(EventAgentStarted), "role", "node")
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(b))), &rec); err != nil {
		t.Fatalf("not one JSON line: %q", b)
	}
	if rec["event"] != EventAgentStarted || rec["role"] != "node" || rec["msg"] != "agent started" {
		t.Errorf("record = %v", rec)
	}
}
