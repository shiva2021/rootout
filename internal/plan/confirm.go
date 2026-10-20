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

// ConfirmWord is what Prompt requires the user to type before anything is deleted.
const ConfirmWord = "delete"

// IsConfirmWord reports whether s, trimmed and case-folded, is exactly ConfirmWord.
func IsConfirmWord(s string) bool {
	return strings.EqualFold(strings.TrimSpace(s), ConfirmWord)
}

// Confirmer decides whether a plan runs. It may change Item.Selected and
// Plan.BrewZap before answering.
type Confirmer interface {
	Confirm(ctx context.Context, p *Plan) (bool, error)
}

// Auto says yes without asking (--yes).
type Auto struct{}

// Confirm implements Confirmer.
func (Auto) Confirm(context.Context, *Plan) (bool, error) { return true, nil }

// Prompt prints the plan and asks the user to type ConfirmWord on a plain terminal.
type Prompt struct {
	In  io.Reader
	Out io.Writer
}

// Confirm implements Confirmer.
func (c Prompt) Confirm(_ context.Context, p *Plan) (bool, error) {
	Describe(c.Out, p)
	_, _ = fmt.Fprintf(c.Out, "\nType %s to continue (anything else cancels): ", ConfirmWord)
	line, err := bufio.NewReader(c.In).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	return IsConfirmWord(line), nil
}

// Describe writes the human summary used by Prompt and by --dry-run.
func Describe(w io.Writer, p *Plan) {
	if p.App.Name == "" {
		_, _ = fmt.Fprintln(w, p.Label)
	} else {
		_, _ = fmt.Fprintf(w, "%s (%s)\n", p.App.Name, p.App.BundleID)
	}
	if p.App.Cask != "" {
		state := "will run first"
		if !p.BrewZap {
			state = "skipped"
		}
		_, _ = fmt.Fprintf(w, "Homebrew cask %q: brew uninstall --zap %s\n", p.App.Cask, state)
	}
	sel := p.Selected()
	byGroup := false
	for _, it := range sel {
		if it.Group != "" {
			byGroup = true
			break
		}
	}
	var cur string
	for i, it := range sel {
		heading := it.Kind.String()
		if byGroup {
			heading = it.Group
		}
		if i == 0 || heading != cur {
			cur = heading
			_, _ = fmt.Fprintf(w, "\n%s\n", heading)
		}
		flags := ""
		if byGroup {
			flags += " [" + it.Kind.String() + "]"
		}
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
