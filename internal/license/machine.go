package license

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/shiva2021/rootout/internal/run"
)

// ErrNoMachineID means the hardware UUID could not be read.
var ErrNoMachineID = errors.New("license: could not read this Mac's id")

const machineSalt = "rootout:"

var uuidRE = regexp.MustCompile(`"IOPlatformUUID" = "([0-9A-Fa-f-]+)"`)

// MachineID returns a stable, salted hash of this Mac's hardware UUID.
// The raw UUID never leaves the machine.
func MachineID(ctx context.Context, r run.Runner) (string, error) {
	out, err := r(ctx, "ioreg", "-rd1", "-c", "IOPlatformExpertDevice")
	if err != nil {
		return "", fmt.Errorf("%w: ioreg: %w", ErrNoMachineID, err)
	}
	m := uuidRE.FindSubmatch(out)
	if m == nil {
		return "", ErrNoMachineID
	}
	sum := sha256.Sum256([]byte(machineSalt + strings.ToUpper(string(m[1]))))
	return hex.EncodeToString(sum[:])[:32], nil
}
