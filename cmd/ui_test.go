package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestUIOpensTheBrowserAndStopsWhenIdle(t *testing.T) {
	oldOpen, oldIdle := openBrowser, uiIdle
	t.Cleanup(func() { openBrowser, uiIdle = oldOpen, oldIdle })
	uiIdle = 300 * time.Millisecond
	opened := make(chan int, 1)
	openBrowser = func(target string) error {
		link, err := linkFrom(target)
		if err != nil {
			t.Error(err)
			opened <- 0
			return nil
		}
		go func() {
			resp, err := http.Get(link)
			if err != nil {
				opened <- 0
				return
			}
			resp.Body.Close()
			opened <- resp.StatusCode
		}()
		return nil
	}
	var out, errOut bytes.Buffer
	done := make(chan int, 1)
	go func() { done <- run([]string{"ui", "--data-dir", t.TempDir()}, &out, &errOut) }()
	select {
	case code := <-opened:
		if code != http.StatusOK {
			t.Errorf("the link answered %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no browser was opened")
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Errorf("exit %d, stderr %q", code, errOut.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sevjala ui did not stop once idle")
	}
	if !strings.Contains(out.String(), "http://127.0.0.1:") {
		t.Errorf("stdout %q", out.String())
	}
}

var refresh = regexp.MustCompile(`url=(http://127\.0\.0\.1:[^"]+)"`)

// linkFrom checks how the link reaches the browser. Other accounts can read a
// command line on macOS and Linux, so there the browser gets a file only this
// account can read.
func linkFrom(target string) (string, error) {
	if runtime.GOOS == "windows" {
		return target, nil
	}
	if strings.Contains(target, "token") || strings.HasPrefix(target, "http") {
		return "", fmt.Errorf("the link with its token went on the command line: %q", target)
	}
	fi, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("launch file mode %v", fi.Mode().Perm())
	}
	b, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	m := refresh.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("no link in the launch file: %s", b)
	}
	return string(m[1]), nil
}
