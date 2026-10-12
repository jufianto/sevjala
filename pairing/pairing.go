// Package pairing holds a node's one-time pairing code and checks the
// controller's HMAC proof (blueprint: Coordination → Pairing).
package pairing

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

const (
	TTL         = 10 * time.Minute
	MaxAttempts = 5
)

var (
	ErrNoCode   = errors.New("no active pairing code; run 'sevjala code' on the node")
	ErrBadProof = errors.New("wrong pairing code")
	ErrBurnt    = errors.New("too many wrong codes; run 'sevjala code' on the node again")
)

// Codes keeps the code in memory only; an agent restart clears it.
type Codes struct {
	mu       sync.Mutex
	now      func() time.Time
	code     string
	expires  time.Time
	attempts int
}

func New(now func() time.Time) *Codes { return &Codes{now: now} }

// Issue replaces any active code.
func (c *Codes) Issue() (string, time.Time, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", time.Time{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.code, c.expires, c.attempts = fmt.Sprintf("%06d", n.Int64()), c.now().Add(TTL), 0
	return c.code, c.expires, nil
}

func (c *Codes) Active() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active()
}

func (c *Codes) active() bool {
	if c.code != "" && !c.now().Before(c.expires) {
		c.code = ""
	}
	return c.code != ""
}

func (c *Codes) Check(controllerFP, nodeFP, proof string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.active() {
		return ErrNoCode
	}
	if hmac.Equal([]byte(Proof(c.code, controllerFP, nodeFP)), []byte(proof)) {
		c.code = ""
		return nil
	}
	c.attempts++
	if c.attempts >= MaxAttempts {
		c.code = ""
		return ErrBurnt
	}
	return ErrBadProof
}

func Proof(code, controllerFP, nodeFP string) string {
	m := hmac.New(sha256.New, []byte(code))
	m.Write([]byte(controllerFP + ":" + nodeFP))
	return hex.EncodeToString(m.Sum(nil))
}

func ValidCode(s string) bool {
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
