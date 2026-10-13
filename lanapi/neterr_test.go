package lanapi

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func dialErr(err error) error {
	return &url.Error{Op: "Get", URL: "https://192.168.1.20:17891/status",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", err)}}
}

func readErr(err error) error {
	return &url.Error{Op: "Put", URL: "https://192.168.1.20:17891/backups/x",
		Err: &net.OpError{Op: "read", Net: "tcp", Err: os.NewSyscallError("read", err)}}
}

func TestPlainLANErrors(t *testing.T) {
	const addr = "192.168.1.20:17891"
	pin := fmt.Errorf("%w: server certificate %s does not match the pinned %s", ErrPinMismatch,
		strings.Repeat("a", 64), strings.Repeat("b", 64))
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"refused", dialErr(refusedErrs[0]), "nothing answers at 192.168.1.20:17891: is Sevjala installed and running on that PC?"},
		{"dial timed out", dialErr(timeoutErrs[0]), "192.168.1.20:17891 did not answer in time: is that PC on and on this network?"},
		{"context deadline", &url.Error{Op: "Get", URL: "https://x/status", Err: context.DeadlineExceeded},
			"192.168.1.20:17891 did not answer in time: is that PC on and on this network?"},
		{"i/o timeout", readErr(os.ErrDeadlineExceeded), "192.168.1.20:17891 did not answer in time: is that PC on and on this network?"},
		{"host unreachable", dialErr(unreachableErrs[0]),
			"cannot reach 192.168.1.20:17891 from this PC: it is on another network, or the router keeps devices apart (client/AP isolation)"},
		{"network unreachable", dialErr(unreachableErrs[1]),
			"cannot reach 192.168.1.20:17891 from this PC: it is on another network, or the router keeps devices apart (client/AP isolation)"},
		{"no such host", &url.Error{Op: "Get", URL: "https://nas-pc:17891/status",
			Err: &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "no such host", Name: "nas-pc", IsNotFound: true}}},
			"cannot find 'nas-pc' on the network; use its IP address instead"},
		{"not TLS", &url.Error{Op: "Get", URL: "https://x/status", Err: tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}},
			"192.168.1.20:17891 is not a Sevjala agent"},
		{"TLS alert", &url.Error{Op: "Get", URL: "https://x/status", Err: &net.OpError{Op: "remote error", Err: errors.New("tls: protocol version not supported")}},
			"192.168.1.20:17891 is not a Sevjala agent"},
		{"no certificate", &url.Error{Op: "Get", URL: "https://x/status", Err: errNoCert}, "192.168.1.20:17891 is not a Sevjala agent"},
		{"plain HTTP", &url.Error{Op: "Get", URL: "https://x/status", Err: http.ErrSchemeMismatch}, "192.168.1.20:17891 is not a Sevjala agent"},
		{"pin mismatch", &url.Error{Op: "Get", URL: "https://x/status", Err: pin},
			"this node's identity changed since pairing (reinstalled, or another PC now has its address): pair it again"},
		{"reset", readErr(resetErrs[0]), "192.168.1.20:17891 closed the connection: try again"},
		{"EOF", &url.Error{Op: "Put", URL: "https://x/backups/x", Err: io.EOF}, "192.168.1.20:17891 closed the connection: try again"},
		{"other", &url.Error{Op: "Get", URL: "https://x/status", Err: fmt.Errorf("wrapped: %w", errors.New("something odd"))},
			"192.168.1.20:17891: something odd"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := plain(addr, tt.err)
			if got.Error() != tt.want {
				t.Errorf("got  %q\nwant %q", got, tt.want)
			}
			if !errors.Is(got, tt.err) {
				t.Errorf("the cause is lost: %v", got)
			}
		})
	}
}

// A real refused dial through the client: plain text, still a dial *net.OpError.
func TestClosedPortIsPlain(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	_, err = controller(t).Status(context.Background(), addr, "sha256:x")
	if want := "nothing answers at " + addr + ": is Sevjala installed and running on that PC?"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
	var op *net.OpError
	if !errors.As(err, &op) || op.Op != "dial" {
		t.Errorf("not a dial error underneath: %#v", err)
	}
}

func TestPlainTLSFromNonSevjalaPeer(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Write([]byte("HTTP/1.1 400 Bad Request\r\n\r\n"))
			c.Close()
		}
	}()
	_, err = controller(t).Identity(context.Background(), l.Addr().String())
	if want := l.Addr().String() + " is not a Sevjala agent"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

func TestIdentityFromNonSevjalaHTTPS(t *testing.T) {
	tests := []struct {
		name string
		h    http.HandlerFunc
		want string
	}{
		{"not found", http.NotFound, " is not a Sevjala agent"},
		{"not JSON", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "<html>") }, " is not a Sevjala agent"},
		{"other fingerprint", func(w http.ResponseWriter, _ *http.Request) {
			io.WriteString(w, `{"agent_id":"x","fp":"sha256:`+strings.Repeat("0", 64)+`"}`)
		}, " answered with a different identity than its certificate: it may not be a Sevjala agent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewTLSServer(tt.h)
			defer s.Close()
			addr := strings.TrimPrefix(s.URL, "https://")
			_, err := controller(t).Identity(context.Background(), addr)
			if err == nil || err.Error() != addr+tt.want {
				t.Errorf("err = %v, want %q", err, addr+tt.want)
			}
		})
	}
}

func TestSilentPeerIsPlainTimeout(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	c := controller(t)
	c.Timeout = 200 * time.Millisecond
	_, err = c.Status(context.Background(), l.Addr().String(), "sha256:x")
	if want := l.Addr().String() + " did not answer in time: is that PC on and on this network?"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}
