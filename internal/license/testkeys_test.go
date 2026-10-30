package license

import (
	"crypto/ed25519"
	"crypto/sha256"
)

// testSeed is shared with the Worker tests so both sides sign the same keys.
var testSeed = sha256.Sum256([]byte("rootout-test-seed"))

var (
	testPriv = ed25519.NewKeyFromSeed(testSeed[:])
	testPub  = testPriv.Public().(ed25519.PublicKey)
)
