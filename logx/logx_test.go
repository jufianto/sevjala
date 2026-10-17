package logx

import (
	"bytes"
	"context"
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

func TestByFromContext(t *testing.T) {
	var buf bytes.Buffer
	l := JSON(&buf)
	l.InfoContext(WithBy(context.Background(), `OFFICE\ana`), "x", Event(EventPolicyChanged))
	l.Info("y")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines = %q", lines)
	}
	var a, b map[string]any
	if json.Unmarshal([]byte(lines[0]), &a) != nil || json.Unmarshal([]byte(lines[1]), &b) != nil {
		t.Fatalf("not JSON: %q", lines)
	}
	if a["by"] != `OFFICE\ana` {
		t.Errorf("by = %v", a["by"])
	}
	if _, ok := b["by"]; ok {
		t.Errorf("a line without a caller has by: %v", b)
	}
	if By(context.Background()) != "" {
		t.Error("By without WithBy")
	}
}
