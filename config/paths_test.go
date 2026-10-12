package config

import (
	"path/filepath"
	"testing"
)

func TestDefaultDataDir(t *testing.T) {
	tests := []struct {
		goos, programData, want string
	}{
		{"windows", `D:\PD`, `D:\PD\Sevjala`},
		{"windows", "", `C:\ProgramData\Sevjala`},
		{"darwin", "", "/Library/Application Support/Sevjala"},
		{"linux", "", "/var/lib/sevjala"},
	}
	for _, tt := range tests {
		if got := defaultDataDir(tt.goos, tt.programData); got != tt.want {
			t.Errorf("defaultDataDir(%q, %q) = %q, want %q", tt.goos, tt.programData, got, tt.want)
		}
	}
}

func TestResolveDataDir(t *testing.T) {
	flagDir := t.TempDir()
	envDir := t.TempDir()

	t.Setenv(EnvDataDir, envDir)
	if got, _ := ResolveDataDir(flagDir); got != flagDir {
		t.Errorf("flag: got %q, want %q", got, flagDir)
	}
	if got, _ := ResolveDataDir(""); got != envDir {
		t.Errorf("env: got %q, want %q", got, envDir)
	}

	t.Setenv(EnvDataDir, "")
	if got, _ := ResolveDataDir(""); got != DefaultDataDir() {
		t.Errorf("default: got %q, want %q", got, DefaultDataDir())
	}

	got, err := ResolveDataDir("rel")
	if err != nil || !filepath.IsAbs(got) {
		t.Errorf("relative flag: got %q, %v; want an absolute path", got, err)
	}
	if !IsDefaultDataDir(DefaultDataDir()) || IsDefaultDataDir(flagDir) {
		t.Error("IsDefaultDataDir wrong")
	}
}
