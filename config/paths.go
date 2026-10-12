package config

import (
	"os"
	"path/filepath"
	"runtime"
)

const (
	EnvDataDir  = "SEVJALA_DATA_DIR"
	SocketGroup = "sevjala"
)

func DefaultDataDir() string {
	return defaultDataDir(runtime.GOOS, os.Getenv("ProgramData"))
}

func defaultDataDir(goos, programData string) string {
	switch goos {
	case "windows":
		if programData == "" {
			programData = `C:\ProgramData`
		}
		return programData + `\Sevjala`
	case "darwin":
		return "/Library/Application Support/Sevjala"
	default:
		return "/var/lib/sevjala"
	}
}

// ResolveDataDir picks the --data-dir flag, then $SEVJALA_DATA_DIR, then the
// OS default, and returns it as an absolute path.
func ResolveDataDir(flag string) (string, error) {
	dir := flag
	if dir == "" {
		dir = os.Getenv(EnvDataDir)
	}
	if dir == "" {
		return DefaultDataDir(), nil
	}
	return filepath.Abs(dir)
}

func IsDefaultDataDir(dir string) bool {
	return filepath.Clean(dir) == filepath.Clean(DefaultDataDir())
}

func Path(dataDir string) string { return filepath.Join(dataDir, FileName) }

func LogDir(dataDir string) string { return filepath.Join(dataDir, "logs") }
