package pairing

import (
	"errors"
	"regexp"
	"testing"
	"time"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

const cfp, nfp = "sha256:c", "sha256:n"

func issued(t *testing.T) (*Codes, *clock, string) {
	t.Helper()
	clk := &clock{t: time.Date(2026, 10, 12, 16, 0, 0, 0, time.UTC)}
	c := New(clk.now)
	code, exp, err := c.Issue()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^\d{6}$`).MatchString(code) || !exp.Equal(clk.t.Add(TTL)) {
		t.Fatalf("code %q expires %v", code, exp)
	}
	return c, clk, code
}

func wrong(code string) string {
	if code == "000000" {
		return "111111"
	}
	return "000000"
}

func TestCorrectProofClearsCode(t *testing.T) {
	c, _, code := issued(t)
	if err := c.Check(cfp, nfp, Proof(code, cfp, nfp)); err != nil {
		t.Fatal(err)
	}
	if err := c.Check(cfp, nfp, Proof(code, cfp, nfp)); !errors.Is(err, ErrNoCode) {
		t.Errorf("second use: %v, want ErrNoCode", err)
	}
}

func TestProofIsBoundToBothFingerprints(t *testing.T) {
	c, _, code := issued(t)
	if err := c.Check(cfp, nfp, Proof(code, "sha256:other", nfp)); !errors.Is(err, ErrBadProof) {
		t.Errorf("proof for another controller: %v", err)
	}
}

func TestFifthWrongProofBurns(t *testing.T) {
	c, _, code := issued(t)
	for i := 1; i < MaxAttempts; i++ {
		if err := c.Check(cfp, nfp, Proof(wrong(code), cfp, nfp)); !errors.Is(err, ErrBadProof) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if err := c.Check(cfp, nfp, Proof(wrong(code), cfp, nfp)); !errors.Is(err, ErrBurnt) {
		t.Fatalf("attempt %d: %v, want ErrBurnt", MaxAttempts, err)
	}
	if err := c.Check(cfp, nfp, Proof(code, cfp, nfp)); !errors.Is(err, ErrNoCode) {
		t.Errorf("right code after burn: %v", err)
	}
}

func TestCodeExpires(t *testing.T) {
	c, clk, code := issued(t)
	clk.t = clk.t.Add(TTL)
	if c.Active() {
		t.Error("active at expiry")
	}
	if err := c.Check(cfp, nfp, Proof(code, cfp, nfp)); !errors.Is(err, ErrNoCode) {
		t.Errorf("expired code: %v", err)
	}
}

func TestReissueReplacesAndResetsAttempts(t *testing.T) {
	c, _, first := issued(t)
	for i := 1; i < MaxAttempts; i++ {
		c.Check(cfp, nfp, Proof(wrong(first), cfp, nfp))
	}
	second, _, _ := c.Issue()
	if second != first {
		if err := c.Check(cfp, nfp, Proof(first, cfp, nfp)); !errors.Is(err, ErrBadProof) {
			t.Errorf("old code after reissue: %v", err)
		}
	}
	if err := c.Check(cfp, nfp, Proof(second, cfp, nfp)); err != nil {
		t.Errorf("new code: %v", err)
	}
}

func TestValidCode(t *testing.T) {
	for s, want := range map[string]bool{"123456": true, "012345": true, "12345": false, "1234567": false, "12a456": false, "": false} {
		if ValidCode(s) != want {
			t.Errorf("ValidCode(%q) = %v", s, !want)
		}
	}
}
