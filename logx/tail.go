package logx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// tailChunk is how much of a log file Tail reads at a time, from the end.
var tailChunk int64 = 64 << 10

// Tail returns up to limit log entries, newest first, from the current file
// and its rotations; lines that are not JSON (a write cut short) are skipped.
// It reads each file backwards and stops once it has limit entries.
func Tail(dir string, limit int) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	path := filepath.Join(dir, FileName)
	for i := 0; i < Keep && len(out) < limit; i++ {
		p := path
		if i > 0 {
			p = fmt.Sprintf("%s.%d", path, i)
		}
		err := tailFile(p, limit, &out)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func tailFile(p string, limit int, out *[]json.RawMessage) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	add := func(l []byte) {
		if l = bytes.TrimSpace(l); len(*out) < limit && json.Valid(l) {
			*out = append(*out, json.RawMessage(bytes.Clone(l)))
		}
	}
	var rest []byte // the start of a line whose end was read already
	for end > 0 && len(*out) < limit {
		n := min(tailChunk, end)
		end -= n
		buf := make([]byte, n, n+int64(len(rest)))
		if _, err := f.ReadAt(buf, end); err != nil {
			return err
		}
		buf = append(buf, rest...)
		first := bytes.IndexByte(buf, '\n')
		if first < 0 {
			rest = buf
			continue
		}
		lines := bytes.Split(buf[first+1:], []byte("\n"))
		for j := len(lines) - 1; j >= 0; j-- {
			add(lines[j])
		}
		rest = buf[:first]
	}
	if end == 0 {
		add(rest)
	}
	return nil
}
