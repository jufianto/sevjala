package cmd

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestUIOpensTheBrowserAndStopsWhenIdle(t *testing.T) {
	oldOpen, oldIdle := openBrowser, uiIdle
	t.Cleanup(func() { openBrowser, uiIdle = oldOpen, oldIdle })
	uiIdle = 300 * time.Millisecond
	opened := make(chan int, 1)
	openBrowser = func(url string) error {
		go func() {
			cl := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			resp, err := cl.Get(url)
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
		if code != http.StatusSeeOther {
			t.Errorf("the link answered %d, want a redirect into a session", code)
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
