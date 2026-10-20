package ollama

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/shiva2021/purge/internal/run"
)

// Hardware is what Recommend needs to know about the Mac.
type Hardware struct {
	Chip         string // machdep.cpu.brand_string, e.g. "Apple M1"
	AppleSilicon bool   // hw.optional.arm64 == 1
	RAMBytes     int64  // hw.memsize
	Cores        int    // hw.ncpu
}

// Probe reads the hardware facts with sysctl. hw.optional.arm64 does not
// exist on older Intel Macs; a failure there means "not Apple Silicon".
func Probe(ctx context.Context, r run.Runner) (Hardware, error) {
	get := func(key string) (string, error) {
		out, err := r(ctx, "sysctl", "-n", key)
		if err != nil {
			return "", fmt.Errorf("ollama: sysctl %s: %w", key, err)
		}
		return strings.TrimSpace(string(out)), nil
	}

	var hw Hardware
	mem, err := get("hw.memsize")
	if err != nil {
		return hw, err
	}
	if hw.RAMBytes, err = strconv.ParseInt(mem, 10, 64); err != nil {
		return hw, fmt.Errorf("ollama: parse hw.memsize %q: %w", mem, err)
	}
	if hw.Chip, err = get("machdep.cpu.brand_string"); err != nil {
		return hw, err
	}
	if arm, err := get("hw.optional.arm64"); err == nil {
		hw.AppleSilicon = arm == "1"
	}
	ncpu, err := get("hw.ncpu")
	if err != nil {
		return hw, err
	}
	if hw.Cores, err = strconv.Atoi(ncpu); err != nil {
		return hw, fmt.Errorf("ollama: parse hw.ncpu %q: %w", ncpu, err)
	}
	return hw, nil
}
