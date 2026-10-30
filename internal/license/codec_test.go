package license

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func patronClaims() Claims {
	return Claims{V: 1, Kind: KindPatron, Sub: "12345678", MID: "0123456789abcdef0123456789abcdef", IAT: 1730246400, EXP: 1733270400}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	want := patronClaims()
	key, err := Encode(want, testPriv)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(key, Prefix+".") {
		t.Errorf("key %q lacks prefix", key)
	}
	got, err := Decode(key, testPub)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestDecodeRejects(t *testing.T) {
	mustEncode := func(c Claims) string {
		k, err := Encode(c, testPriv)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	good := mustEncode(patronClaims())
	parts := strings.Split(good, ".")
	tampered := []byte(parts[1])
	if tampered[3] == 'A' {
		tampered[3] = 'B'
	} else {
		tampered[3] = 'A'
	}
	otherPub, _, _ := ed25519.GenerateKey(nil)

	with := func(f func(*Claims)) string {
		c := patronClaims()
		f(&c)
		return mustEncode(c)
	}
	tests := []struct {
		name string
		key  string
		pub  ed25519.PublicKey
	}{
		{"wrong prefix", "ro2." + parts[1] + "." + parts[2], testPub},
		{"two segments", parts[0] + "." + parts[1], testPub},
		{"bad base64", parts[0] + ".!!!." + parts[2], testPub},
		{"tampered payload", parts[0] + "." + string(tampered) + "." + parts[2], testPub},
		{"wrong public key", good, otherPub},
		{"v=2", with(func(c *Claims) { c.V = 2 }), testPub},
		{"unknown kind", with(func(c *Claims) { c.Kind = "x" }), testPub},
		{"short mid", with(func(c *Claims) { c.MID = "0123456789" }), testPub},
		{"patron without exp", with(func(c *Claims) { c.EXP = 0 }), testPub},
		{"exp before iat", with(func(c *Claims) { c.EXP = c.IAT - 1 }), testPub},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Decode(tt.key, tt.pub)
			if !errors.Is(err, ErrBadKey) {
				t.Errorf("err = %v, want ErrBadKey", err)
			}
		})
	}
}

func TestDecodeBadPublicKeyLength(t *testing.T) {
	key, err := Encode(patronClaims(), testPriv)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{0, 31} {
		if _, err := Decode(key, make(ed25519.PublicKey, n)); !errors.Is(err, ErrBadKey) {
			t.Errorf("len %d: err = %v, want ErrBadKey", n, err)
		}
	}
}

func TestEmbeddedPublicKeyEmpty(t *testing.T) {
	if PublicKeyHex != "" {
		t.Skip("key embedded")
	}
	if _, err := EmbeddedPublicKey(); !errors.Is(err, ErrBadKey) {
		t.Errorf("err = %v, want ErrBadKey", err)
	}
}

func TestExpiresAdminNone(t *testing.T) {
	admin := Claims{V: 1, Kind: KindAdmin, Sub: "a", MID: patronClaims().MID, IAT: 1}
	if _, ok := admin.Expires(); ok {
		t.Error("admin Expires ok = true, want false")
	}
	if err := admin.Valid(); err != nil {
		t.Errorf("admin without exp should be valid: %v", err)
	}
	tm, ok := patronClaims().Expires()
	if !ok || tm.Unix() != 1733270400 {
		t.Errorf("patron Expires = %v, %v", tm, ok)
	}
}

type golden struct {
	SeedHex string `json:"seed_hex"`
	PubHex  string `json:"pub_hex"`
	Key     string `json:"key"`
	Claims  Claims `json:"claims"`
}

func TestGoldenKey(t *testing.T) {
	const path = "testdata/golden.json"
	if *update {
		key, err := Encode(patronClaims(), testPriv)
		if err != nil {
			t.Fatal(err)
		}
		g := golden{SeedHex: hex.EncodeToString(testSeed[:]), PubHex: hex.EncodeToString(testPub), Key: key, Claims: patronClaims()}
		b, err := json.MarshalIndent(g, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil { //nolint:gosec // test fixture
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path) //nolint:gosec // fixed test path
	if err != nil {
		t.Fatal(err)
	}
	var g golden
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	pub, err := hex.DecodeString(g.PubHex)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decode(g.Key, ed25519.PublicKey(pub))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, g.Claims) {
		t.Errorf("got %+v, want %+v", got, g.Claims)
	}
	// Encoding is deterministic, so the committed key must match a fresh one.
	fresh, _ := Encode(g.Claims, testPriv)
	if fresh != g.Key {
		t.Error("golden key differs from a fresh encode; run with -update if intended")
	}
}
