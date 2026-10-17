package logx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Tail returns up to limit log entries, newest first, from the current file
// and its rotations; lines that are not JSON (a write cut short) are skipped.
func Tail(dir string, limit int) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	path := filepath.Join(dir, FileName)
	for i := 0; i < Keep && len(out) < limit; i++ {
		p := path
		if i > 0 {
			p = fmt.Sprintf("%s.%d", path, i)
		}
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
		lines := bytes.Split(b, []byte("\n"))
		for j := len(lines) - 1; j >= 0 && len(out) < limit; j-- {
			if l := bytes.TrimSpace(lines[j]); json.Valid(l) {
				out = append(out, json.RawMessage(l))
			}
		}
	}
	return out, nil
}
