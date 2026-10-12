// Package identity is the agent's persistent self: agent_id and a self-signed
// certificate whose fingerprint peers pin (blueprint: Coordination → Identity).
package identity

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	DirName  = "identity"
	idFile   = "agent_id"
	certFile = "cert.pem"
	keyFile  = "key.pem"
	validity = 10 * 365 * 24 * time.Hour
)

type Identity struct {
	AgentID string
	Cert    tls.Certificate
	FP      string
}

func Dir(dataDir string) string { return filepath.Join(dataDir, DirName) }

func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// LoadOrCreate creates a new identity only when agent_id does not exist; any
// other problem is an error so an identity is never silently replaced.
func LoadOrCreate(dataDir string) (*Identity, bool, error) {
	id, err := Load(dataDir)
	if err == nil {
		return id, false, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	if err := create(dataDir); err != nil {
		return nil, false, fmt.Errorf("create identity: %w", err)
	}
	id, err = Load(dataDir)
	return id, err == nil, err
}

// Load wraps fs.ErrNotExist only when agent_id is missing.
func Load(dataDir string) (*Identity, error) {
	dir := Dir(dataDir)
	raw, err := os.ReadFile(filepath.Join(dir, idFile))
	if err != nil {
		return nil, fmt.Errorf("identity: %w", err)
	}
	agentID := strings.TrimSpace(string(raw))
	cert, err := tls.LoadX509KeyPair(filepath.Join(dir, certFile), filepath.Join(dir, keyFile))
	if err != nil {
		return nil, fmt.Errorf("identity in %s is unusable: %s", dir, err.Error())
	}
	if cert.Leaf == nil {
		if cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0]); err != nil {
			return nil, fmt.Errorf("identity in %s is unusable: %s", dir, err.Error())
		}
	}
	if cn := cert.Leaf.Subject.CommonName; cn != agentID {
		return nil, fmt.Errorf("identity in %s is unusable: certificate CN %q does not match agent_id %q", dir, cn, agentID)
	}
	return &Identity{AgentID: agentID, Cert: cert, FP: Fingerprint(cert.Leaf.Raw)}, nil
}

// create writes agent_id last, so a crash part-way leaves "no identity yet".
func create(dataDir string) error {
	dir := Dir(dataDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	agentID, err := newUUID()
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: agentID},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, keyFile), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(dir, certFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	return writeFile(filepath.Join(dir, idFile), []byte(agentID+"\n"), 0o644)
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
