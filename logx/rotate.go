package logx

import (
	"fmt"
	"os"
	"sync"
)

// rename is swapped in tests to simulate a log file held open on Windows.
var rename = os.Rename

// RotatingWriter appends to path and, when a write would pass maxBytes,
// shifts path → path.1 → … → path.<keep-1>, dropping the oldest.
type RotatingWriter struct {
	mu   sync.Mutex
	path string
	max  int64
	keep int
	f    *os.File
	size int64
}

func NewRotatingWriter(path string, maxBytes int64, keep int) (*RotatingWriter, error) {
	w := &RotatingWriter{path: path, max: maxBytes, keep: keep}
	if err := w.open(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) open() error {
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	w.f, w.size = f, info.Size()
	return nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		if err := w.open(); err != nil {
			return 0, err
		}
	}
	if w.size > 0 && w.size+int64(len(p)) > w.max {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate closes before renaming because Windows cannot rename an open file.
// The current file is moved aside first; if that fails (another process holds
// it), the old logs are left alone and rotation is retried after another
// maxBytes.
func (w *RotatingWriter) rotate() error {
	w.f.Close()
	w.f = nil
	moved := w.path + ".rotating"
	if err := rename(w.path, moved); err != nil {
		if err := w.open(); err != nil {
			return err
		}
		w.size = 0
		return nil
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", w.path, w.keep-1))
	for i := w.keep - 2; i >= 1; i-- {
		_ = rename(fmt.Sprintf("%s.%d", w.path, i), fmt.Sprintf("%s.%d", w.path, i+1))
	}
	_ = rename(moved, w.path+".1")
	return w.open()
}

func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	return err
}
