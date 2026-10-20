package plan

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shiva2021/rootout/internal/leftover"
	"github.com/shiva2021/rootout/internal/output"
)

// Confirmer decides whether a plan runs. It may change Item.Selected and
// Plan.BrewZap before answering.
type Confirmer interface {
	Confirm(ctx context.Context, p *Plan) (bool, error)
}

// Auto says yes without asking (--yes).
type Auto struct{}

// Confirm implements Confirmer.
func (Auto) Confirm(context.Context, *Plan) (bool, error) { return true, nil }

// Prompt prints the plan and asks y/N on a plain terminal.
type Prompt struct {
	In  io.Reader
	Out io.Writer
}

// Confirm implements Confirmer.
func (c Prompt) Confirm(_ context.Context, p *Plan) (bool, error) {
	Describe(c.Out, p)
	_, _ = fmt.Fprintf(c.Out, "\nProceed (%s)? [y/N] ", p.Mode)
	line, err := bufio.NewReader(c.In).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return true, nil
	}
	return false, nil
}

// Describe writes the human summary used by Prompt and by --dry-run.
func Describe(w io.Writer, p *Plan) {
	_, _ = fmt.Fprintf(w, "%s (%s)\n", p.App.Name, p.App.BundleID)
	if p.App.Cask != "" {
		state := "will run first"
		if !p.BrewZap {
			state = "skipped"
		}
		_, _ = fmt.Fprintf(w, "Homebrew cask %q: brew uninstall --zap %s\n", p.App.Cask, state)
	}
	var cur leftover.Kind = -1
	for _, it := range p.Selected() {
		if it.Kind != cur {
			cur = it.Kind
			_, _ = fmt.Fprintf(w, "\n%s\n", it.Kind)
		}
		flags := ""
		if it.NeedsRoot {
			flags += " [admin]"
		}
		if it.MatchedBy == leftover.ByName || it.MatchedBy == ByManual {
			flags += " [" + it.MatchedBy + "]"
		}
		size := ""
		if isPath(it.Leftover) {
			size = output.Bytes(it.SizeBytes)
		}
		_, _ = fmt.Fprintf(w, "  %-9s %s%s\n", size, it.Path, flags)
		if it.Note != "" {
			_, _ = fmt.Fprintf(w, "            %s\n", it.Note)
		}
	}
	n, bytes, root := p.Totals()
	_, _ = fmt.Fprintf(w, "\n%d paths, %s", n, output.Bytes(bytes))
	if root > 0 {
		_, _ = fmt.Fprintf(w, "; %d need admin rights and are deleted permanently", root)
	}
	_, _ = fmt.Fprintln(w)
}
