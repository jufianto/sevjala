package tray

import "os"

// binaryWatch notices 'sevjala update' replacing the installed binary, so a
// tray moves to the new one: on Windows the old file stays locked while any
// tray runs from it.
type binaryWatch struct {
	path  string
	start os.FileInfo
}

func watchBinary(path string) binaryWatch {
	fi, _ := os.Stat(path)
	return binaryWatch{path: path, start: fi}
}

func (w binaryWatch) replaced() bool {
	fi, err := os.Stat(w.path)
	return err == nil && w.start != nil && !os.SameFile(fi, w.start)
}
