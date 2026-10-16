package lanapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jufianto/sevjala/identity"
	"github.com/jufianto/sevjala/inventory"
	"github.com/jufianto/sevjala/pairing"
)

var (
	ErrRejected    = errors.New("rejected this controller's certificate (not paired with it)")
	ErrPinMismatch = errors.New("node certificate changed since pairing")
)

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
	Stall   time.Duration // PutBackup gives up after this long without progress
}

func NewClient(self *identity.Identity) *Client {
	return &Client{self: self, Timeout: 10 * time.Second, Stall: time.Minute}
}

// Identity presents no client cert, so it works against a node paired with
// another controller.
func (c *Client) Identity(ctx context.Context, addr string) (IdentityInfo, error) {
	var tlsFP string
	var info IdentityInfo
	if err := c.do(ctx, c.http("", false, &tlsFP), http.MethodGet, addr, "/identity", nil, &info); err != nil {
		// Any agent answers /identity: an error status or a body that is not JSON comes from something else.
		var le *lanError
		if !errors.As(err, &le) {
			err = &lanError{msg: addr + " is not a Sevjala agent", err: err}
		}
		return IdentityInfo{}, err
	}
	if info.FP != tlsFP {
		return IdentityInfo{}, fmt.Errorf("%s answered with a different identity than its certificate: it may not be a Sevjala agent", addr)
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

// DeleteBackup asks a node to remove a backup; deleted is false when it was not there.
func (c *Client) DeleteBackup(ctx context.Context, addr, nodeFP, id, reason string) (bool, error) {
	var out DeleteResponse
	err := c.do(ctx, c.http(nodeFP, true, nil), http.MethodDelete, addr, "/backups/"+url.PathEscape(id), DeleteRequest{Reason: reason}, &out)
	return out.Deleted, err
}

func (c *Client) http(wantFP string, withCert bool, gotFP *string) *http.Client {
	return &http.Client{Timeout: c.Timeout, Transport: c.transport(wantFP, withCert, gotFP)}
}

// transport pins the server to wantFP (any server when empty) and records the
// fingerprint it saw in gotFP.
func (c *Client) transport(wantFP string, withCert bool, gotFP *string) *http.Transport {
	cfg := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // no CA: VerifyPeerCertificate pins the fingerprint instead
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errNoCert
			}
			fp := identity.Fingerprint(raw[0])
			if gotFP != nil {
				*gotFP = fp
			}
			if wantFP != "" && fp != wantFP {
				return fmt.Errorf("%w: server certificate %s does not match the pinned %s", ErrPinMismatch, fp, wantFP)
			}
			return nil
		},
	}
	if withCert {
		cfg.Certificates = []tls.Certificate{c.self.Cert}
	}
	return &http.Transport{TLSClientConfig: cfg, DisableKeepAlives: true}
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
		return plain(addr, err)
	}
	return send(hc, req, addr, out)
}

func send(hc *http.Client, req *http.Request, addr string, out any) error {
	resp, err := roundTrip(hc, req, addr)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(out)
}

// roundTrip returns the response of a 200; anything else is an error.
func roundTrip(hc *http.Client, req *http.Request, addr string) (*http.Response, error) {
	resp, err := hc.Do(req)
	if err != nil {
		// The node's alert is not a typed error; this is the only signal.
		if strings.Contains(err.Error(), "tls: bad certificate") {
			return nil, fmt.Errorf("%s %w", addr, ErrRejected)
		}
		return nil, plain(addr, err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		var e struct {
			Error string `json:"error"`
		}
		if json.NewDecoder(io.LimitReader(resp.Body, maxBody)).Decode(&e) != nil || e.Error == "" {
			e.Error = resp.Status
		}
		return nil, &HTTPError{Code: resp.StatusCode, Msg: e.Error}
	}
	return resp, nil
}

var errStalled = errors.New("transfer stalled: no progress for too long")

// PutBackup streams body to the node pinned by nodeFP. There is no overall
// timeout (files are hundreds of MB); a watchdog cancels when no bytes moved
// for c.Stall.
func (c *Client) PutBackup(ctx context.Context, addr, nodeFP string, m inventory.Manifest, body io.Reader) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	pr := &progressReader{r: body}
	defer watchStall(cancel, pr, c.Stall)()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "https://"+addr+"/backups/"+url.PathEscape(m.BackupID), pr)
	if err != nil {
		return err
	}
	req.ContentLength = m.Size
	req.Header.Set("Expect", "100-continue")
	req.Header.Set(HeaderSHA256, m.SHA256)
	req.Header.Set(HeaderSize, strconv.FormatInt(m.Size, 10))
	req.Header.Set(HeaderManifest, base64.StdEncoding.EncodeToString(raw))
	tr := c.transport(nodeFP, true, nil)
	tr.ExpectContinueTimeout = 5 * time.Second
	// After the last byte the node still hashes, fsyncs and renames; a node that never
	// answers would otherwise hold this worker forever.
	tr.ResponseHeaderTimeout = 5 * c.Stall
	var out PutResponse
	if err := send(&http.Client{Transport: tr}, req, addr, &out); err != nil {
		if errors.Is(context.Cause(ctx), errStalled) {
			return fmt.Errorf("%s: %w", addr, errStalled)
		}
		return err
	}
	if out.SHA256 != m.SHA256 {
		return fmt.Errorf("%s stored sha256 %s, expected %s", addr, out.SHA256, m.SHA256)
	}
	return nil
}

type progressReader struct {
	r    io.Reader
	last atomic.Int64
	done atomic.Bool
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.last.Store(time.Now().UnixNano())
	if err == io.EOF {
		p.done.Store(true)
	}
	return n, err
}

// watchStall cancels with errStalled when p has not moved for stall; call
// the returned func to stop watching.
func watchStall(cancel context.CancelCauseFunc, p *progressReader, stall time.Duration) func() {
	p.last.Store(time.Now().UnixNano())
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(stall / 4)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if !p.done.Load() && time.Since(time.Unix(0, p.last.Load())) > stall {
					cancel(errStalled)
					return
				}
			}
		}
	}()
	return func() { close(stop) }
}

// GetBackup streams a stored backup from the node pinned by nodeFP. Like
// PutBackup it has no overall timeout; a read stalled for c.Stall fails.
// The manifest comes from the response headers and must name id.
func (c *Client) GetBackup(ctx context.Context, addr, nodeFP, id string) (inventory.Manifest, io.ReadCloser, error) {
	ctx, cancel := context.WithCancelCause(ctx)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+"/backups/"+url.PathEscape(id), nil)
	if err != nil {
		cancel(nil)
		return inventory.Manifest{}, nil, plain(addr, err)
	}
	tr := c.transport(nodeFP, true, nil)
	tr.ResponseHeaderTimeout = 5 * c.Stall
	resp, err := roundTrip(&http.Client{Transport: tr}, req, addr)
	if err != nil {
		cancel(nil)
		return inventory.Manifest{}, nil, err
	}
	m, sha, size, err := backupHeaders(resp.Header, id)
	if err == nil && (sha != m.SHA256 || size != m.Size) {
		err = fmt.Errorf("%s sent headers that do not match its manifest", addr)
	}
	if err != nil {
		resp.Body.Close()
		cancel(nil)
		return inventory.Manifest{}, nil, err
	}
	pr := &progressReader{r: resp.Body}
	stop := watchStall(cancel, pr, c.Stall)
	return m, &streamBody{progressReader: pr, close: func() error {
		stop()
		cancel(nil)
		return resp.Body.Close()
	}}, nil
}

type streamBody struct {
	*progressReader
	close func() error
}

func (s *streamBody) Close() error { return s.close() }
