package lanapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/pairing"
)

var ErrRejected = errors.New("rejected this controller's certificate (not paired with it)")

type HTTPError struct {
	Code int
	Msg  string
}

func (e *HTTPError) Error() string { return e.Msg }

// Client is the controller side. It never trusts a CA: every server is pinned
// by fingerprint.
type Client struct {
	self    *identity.Identity
	Timeout time.Duration
}

func NewClient(self *identity.Identity) *Client {
	return &Client{self: self, Timeout: 10 * time.Second}
}

// Identity presents no client cert, so it works against a node paired with
// another controller.
func (c *Client) Identity(ctx context.Context, addr string) (IdentityInfo, error) {
	var tlsFP string
	var info IdentityInfo
	if err := c.do(ctx, c.http("", false, &tlsFP), http.MethodGet, addr, "/identity", nil, &info); err != nil {
		return IdentityInfo{}, err
	}
	if info.FP != tlsFP {
		return IdentityInfo{}, fmt.Errorf("%s: /identity says %s but its certificate is %s", addr, info.FP, tlsFP)
	}
	return info, nil
}

func (c *Client) Pair(ctx context.Context, addr, nodeFP, code string) (PairResponse, error) {
	req := PairRequest{ControllerID: c.self.AgentID, ControllerFP: c.self.FP, Proof: pairing.Proof(code, c.self.FP, nodeFP)}
	var out PairResponse
	err := c.do(ctx, c.http(nodeFP, true, nil), http.MethodPost, addr, "/pair", req, &out)
	return out, err
}

func (c *Client) Status(ctx context.Context, addr, nodeFP string) (Status, error) {
	var st Status
	err := c.do(ctx, c.http(nodeFP, true, nil), http.MethodGet, addr, "/status", nil, &st)
	return st, err
}

// http pins the server to wantFP (any server when empty) and records the
// fingerprint it saw in gotFP.
func (c *Client) http(wantFP string, withCert bool, gotFP *string) *http.Client {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // no CA: VerifyPeerCertificate pins the fingerprint instead
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("server sent no certificate")
			}
			fp := identity.Fingerprint(raw[0])
			if gotFP != nil {
				*gotFP = fp
			}
			if wantFP != "" && fp != wantFP {
				return fmt.Errorf("server certificate %s does not match the pinned %s", fp, wantFP)
			}
			return nil
		},
	}
	if withCert {
		cfg.Certificates = []tls.Certificate{c.self.Cert}
	}
	return &http.Client{Timeout: c.Timeout, Transport: &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}}
}

func (c *Client) do(ctx context.Context, hc *http.Client, method, addr, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "https://"+addr+path, rd)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req)
	if err != nil {
		// The node's alert is not a typed error; this is the only signal.
		if strings.Contains(err.Error(), "tls: bad certificate") {
			return fmt.Errorf("%s %w", addr, ErrRejected)
		}
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&e) != nil || e.Error == "" {
			e.Error = resp.Status
		}
		return &HTTPError{Code: resp.StatusCode, Msg: e.Error}
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
