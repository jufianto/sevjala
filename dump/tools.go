// Package dump finds the MySQL/MariaDB tools, detects databases and streams
// one mysqldump into a gzip'd, hashed staging file (blueprint: Backup engine).
package dump

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type Tools struct {
	Dumper string `json:"dumper"`
	Client string `json:"client"`
}

func xamppBin(goos string) string {
	switch goos {
	case "windows":
		return `C:\xampp\mysql\bin`
	case "darwin":
		return "/Applications/XAMPP/xamppfiles/bin"
	}
	return "/opt/lampp/bin"
}

// FindTools picks the dumper (the configured path, else XAMPP's, else
// mysqldump or mariadb-dump on PATH) and the mysql client next to it, which
// detection and restore use instead of a Go MySQL driver.
func FindTools(configured string) (Tools, error) {
	return findTools(configured, runtime.GOOS, exec.LookPath, fileExists)
}

func findTools(configured, goos string, lookPath func(string) (string, error), exists func(string) bool) (Tools, error) {
	exe := func(name string) string {
		if goos == "windows" {
			return name + ".exe"
		}
		return name
	}
	xampp := xamppBin(goos)
	var dumper string
	switch {
	case configured != "":
		if !exists(configured) {
			return Tools{}, fmt.Errorf("dumper %s does not exist; fix it with 'sevjala db set --dumper <path>'", configured)
		}
		dumper = configured
	case exists(filepath.Join(xampp, exe("mysqldump"))):
		dumper = filepath.Join(xampp, exe("mysqldump"))
	default:
		for _, name := range []string{"mysqldump", "mariadb-dump"} {
			if p, err := lookPath(name); err == nil {
				dumper = p
				break
			}
		}
	}
	if dumper == "" {
		return Tools{}, fmt.Errorf("mysqldump not found in %s or on PATH; point at it with 'sevjala db set --dumper <path>'", xampp)
	}
	dir := filepath.Dir(dumper)
	for _, name := range []string{"mysql", "mariadb"} {
		if p := filepath.Join(dir, exe(name)); exists(p) {
			return Tools{Dumper: dumper, Client: p}, nil
		}
	}
	for _, name := range []string{"mysql", "mariadb"} {
		if p, err := lookPath(name); err == nil {
			return Tools{Dumper: dumper, Client: p}, nil
		}
	}
	return Tools{}, fmt.Errorf("mysql client not found next to %s or on PATH", dumper)
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
