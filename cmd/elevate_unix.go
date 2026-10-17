//go:build !windows

package cmd

import (
	"errors"
	"os"
	"os/exec"
)

const elevationHow = "asking sudo…"

func elevated(args []string) (int, error) {
	sudo, err := exec.LookPath("sudo")
	if err != nil {
		return 1, errors.New("this needs administrator rights: run with sudo")
	}
	exe, err := os.Executable()
	if err != nil {
		return 1, err
	}
	c := exec.Command(sudo, append([]string{exe}, args...)...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	err = c.Run()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode(), nil
	}
	return 0, err
}
