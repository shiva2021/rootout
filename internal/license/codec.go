package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// Prefix starts every licence key and names the format version.
const Prefix = "ro1"

// PublicKeyHex is the hex Ed25519 public key that verifies keys in release
// builds. Empty means no key is embedded and every Decode fails closed.
const PublicKeyHex = ""

// EmbeddedPublicKey returns PublicKeyHex as a key, or ErrBadKey if it is unset or malformed.
func EmbeddedPublicKey() (ed25519.PublicKey, error) {
	b, err := hex.DecodeString(PublicKeyHex)
	if err != nil || len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: no valid embedded public key", ErrBadKey)
	}
	return ed25519.PublicKey(b), nil
}

var b64 = base64.RawURLEncoding

// Encode signs c and returns the key string.
func Encode(c Claims, priv ed25519.PrivateKey) (string, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("license: private key has %d bytes", len(priv))
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("license: encode claims: %w", err)
	}
	sig := ed25519.Sign(priv, payload)
	return Prefix + "." + b64.EncodeToString(payload) + "." + b64.EncodeToString(sig), nil
}

// Decode verifies key against pub and returns its claims. Every failure wraps ErrBadKey.
func Decode(key string, pub ed25519.PublicKey) (Claims, error) {
	if len(pub) != ed25519.PublicKeySize {
		return Claims{}, fmt.Errorf("%w: bad public key length", ErrBadKey)
	}
	parts := strings.Split(strings.TrimSpace(key), ".")
	if len(parts) != 3 || parts[0] != Prefix {
		return Claims{}, fmt.Errorf("%w: wrong format", ErrBadKey)
	}
	payload, err := b64.DecodeString(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: payload encoding", ErrBadKey)
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil {
		return Claims{}, fmt.Errorf("%w: signature encoding", ErrBadKey)
	}
	if !ed25519.Verify(pub, payload, sig) {
		return Claims{}, fmt.Errorf("%w: signature mismatch", ErrBadKey)
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, fmt.Errorf("%w: payload: %w", ErrBadKey, err)
	}
	if err := c.Valid(); err != nil {
		return Claims{}, err
	}
	return c, nil
}
