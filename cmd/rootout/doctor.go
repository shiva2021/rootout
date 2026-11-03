package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/config"
	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/keychain"
	"github.com/shiva2021/rootout/internal/llm/ollama"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/run"
	"github.com/shiva2021/rootout/internal/tcc"
	"github.com/shiva2021/rootout/internal/trash"
	"github.com/shiva2021/rootout/internal/update"
)

// brewUpgrade is the fix for a Homebrew install; brew update first, because
// a stale tap clone reports "already installed" (docs/kb/release.md).
const brewUpgrade = "brew update && brew upgrade --cask rootout"

// check is one doctor line.
type check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func newDoctorCmd(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the environment rootout depends on",
		Long: `Check what rootout can use on this Mac: the installed rootout version, the macOS version, how files reach
the Trash, Full Disk Access for this terminal, Homebrew, the VS Code-family editors and their command-line tools,
the config file, stored API keys and Ollama with this Mac's chip and RAM. It also asks GitHub for the
latest rootout release (one request, 5 s timeout; off with "rootout config set updates.check false")
and remembers the answer under ~/.cache/rootout/update.json so the home screen can reuse it.

Each line is a check mark or a "!" with a fix. Doctor only reads this Mac; apart
from that one record file it changes nothing and installs nothing.

--json prints [{name, ok, detail, fix}].`,
		Example: `  rootout doctor
  rootout doctor --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := runChecks(cmd.Context())
			// The update row is doctor's alone: runChecks also serves the
			// guide, which must stay offline.
			checks = slices.Insert(checks, 1, updateRow(cmd.Context()))
			w := output.New(os.Stdout, g.json)
			if g.json {
				return w.Emit(checks)
			}
			okMark, warnMark := "✔", "!"
			if isTerminal(os.Stdout) {
				okMark, warnMark = "\033[32m✔\033[0m", "\033[33m!\033[0m"
			}
			for _, c := range checks {
				mark := okMark
				if !c.OK {
					mark = warnMark
				}
				fmt.Printf("%s %-18s %s\n", mark, c.Name, c.Detail)
				if !c.OK && c.Fix != "" {
					fmt.Printf("  %-18s → %s\n", "", c.Fix)
				}
			}
			return nil
		},
	}
}

func runChecks(ctx context.Context) []check {
	var out []check
	add := func(c check) { out = append(out, c) }

	// Installed version first, so "is this the latest?" is answered at a glance.
	add(check{Name: "rootout", OK: true, Detail: version})

	if v, err := run.Default(ctx, "sw_vers", "-productVersion"); err == nil {
		ver := strings.TrimSpace(string(v))
		add(check{Name: "macOS", OK: !strings.HasPrefix(ver, "13.") && !strings.HasPrefix(ver, "12."), Detail: ver, Fix: "rootout supports macOS 14 and later"})
	}

	tm := trash.New()
	backend := tm.Available()
	add(check{Name: "Trash", OK: true, Detail: map[string]string{
		trash.BackendTrash:  "/usr/bin/trash",
		trash.BackendFinder: "Finder (asks for Automation permission once)",
	}[backend]})

	env, err := fsx.Real()
	if err == nil {
		term := tcc.TerminalApp(os.Getenv)
		switch tcc.FullDiskAccess(env) {
		case tcc.Granted:
			add(check{Name: "Full Disk Access", OK: true, Detail: "granted to " + term})
		case tcc.Denied:
			add(check{Name: "Full Disk Access", OK: false, Detail: "not granted to " + term + " (app containers under ~/Library/Containers cannot be removed)", Fix: tcc.SettingsPath + " → add " + term})
		default:
			add(check{Name: "Full Disk Access", OK: true, Detail: "could not tell (no protected folder to probe)"})
		}
	}

	add(check{Name: "Homebrew", OK: hasBrew(), Detail: map[bool]string{true: "found (cask uninstalls use brew --zap)", false: "not found (cask apps are removed by rootout's own sweep)"}[hasBrew()]})

	if err == nil {
		eds := ext.Editors(env, ext.Options{})
		if len(eds) == 0 {
			add(check{Name: "Editors", OK: true, Detail: "no VS Code-family editor found"})
		}
		for _, e := range eds {
			c := check{Name: e.Name, OK: e.CLI != "", Detail: e.ExtDir}
			if e.CLI != "" {
				c.Detail += " (cli: " + e.CLI + ")"
			} else {
				c.Fix = "install its command-line tool for clean uninstalls (Command Palette → \"Shell Command: Install … in PATH\")"
			}
			add(c)
		}
	}

	c, path, cerr := loadConfig()
	if cerr != nil {
		add(check{Name: "Config", OK: false, Detail: cerr.Error(), Fix: "fix or delete " + path})
	} else {
		prov := c.LLM.Provider
		if prov == "" {
			prov = "none (heuristics only)"
		}
		add(check{Name: "Config", OK: true, Detail: path + " · provider " + prov})
	}

	store := keychain.New()
	for _, p := range hostedProviders {
		k, src, err := keychain.Resolve(store, p, os.Getenv)
		switch {
		case err == nil:
			add(check{Name: p + " key", OK: true, Detail: keychain.Mask(k) + " from " + src})
		case errors.Is(err, keychain.ErrNotFound):
			add(check{Name: p + " key", OK: c.LLM.Provider != p, Detail: "not set", Fix: "rootout config set-key " + p})
		default:
			add(check{Name: p + " key", OK: false, Detail: err.Error()})
		}
	}

	hw, herr := ollama.Probe(ctx, run.Default)
	tier := ""
	if herr == nil {
		tier = fmt.Sprintf("%s, %d GB RAM", hw.Chip, hw.RAMBytes>>30)
	}
	ver, oerr := ollamaClient(c).Detect(ctx)
	switch {
	case oerr == nil:
		installed, _ := ollamaClient(c).ListInstalled(ctx, hw)
		add(check{Name: "Ollama", OK: true, Detail: fmt.Sprintf("%s running, %d model(s); %s", ver, len(installed), tier)})
	case errors.Is(oerr, ollama.ErrNotRunning):
		add(check{Name: "Ollama", OK: c.LLM.Provider != "ollama", Detail: "installed, not running; " + tier, Fix: "rootout config models --pick"})
	default:
		add(check{Name: "Ollama", OK: c.LLM.Provider != "ollama", Detail: "not installed (optional, for local models); " + tier, Fix: "rootout config models --pick"})
	}
	return out
}

// updateRow builds the Update check from the real environment.
func updateRow(ctx context.Context) check {
	enabled := config.Default().Updates.Check
	if c, _, err := loadConfig(); err == nil {
		enabled = c.Updates.Check // an unreadable config keeps the default, not "off"
	}
	recordPath := ""
	if env, err := fsx.Real(); err == nil {
		recordPath = update.Path(env, os.Getenv)
	}
	return updateCheck(ctx, update.New(version), enabled, version, recordPath, executable())
}

// executable is the resolved path of this binary, "" when unknown.
func executable() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		return real
	}
	return exe
}

// updateCheck asks GitHub for the latest release and compares it with
// installed. A disabled check, an unknown build or an unreachable GitHub
// is OK (nothing for the user to fix); a newer release is a "!" row whose
// fix is the upgrade command. A successful answer is recorded at
// recordPath for the home screen when recordPath is not empty.
func updateCheck(ctx context.Context, cl *update.Client, enabled bool, installed, recordPath, exe string) check {
	row := check{Name: "Update", OK: true}
	if !enabled {
		row.Detail = "off (rootout config set updates.check true)"
		return row
	}
	if _, ok := update.Installed(installed); !ok {
		row.Detail = "dev build; cannot compare with the latest release"
		return row
	}
	rel, err := cl.Latest(ctx)
	if err != nil {
		row.Detail = "could not reach GitHub: " + err.Error()
		return row
	}
	if recordPath != "" {
		if err := update.WriteRecord(recordPath, rel, time.Now()); err != nil {
			slog.Debug("doctor: update record", "path", recordPath, "err", err)
		}
	}
	// Screens print the parsed version, never the raw tag.
	latest := update.Canonical(rel.Tag)
	if latest == "" {
		row.Detail = "could not read the latest release tag"
		return row
	}
	if !update.Newer(rel.Tag, installed) {
		row.Detail = "up to date (latest " + latest + ")"
		return row
	}
	row.OK = false
	row.Detail = fmt.Sprintf("%s available (installed %s", latest, installed)
	if !rel.PublishedAt.IsZero() {
		row.Detail += ", released " + rel.PublishedAt.Local().Format("2006-01-02")
	}
	row.Detail += ")"
	row.Fix = upgradeHint(exe, rel.URL)
	return row
}

// upgradeHint is the way to install a newer release: the brew command when
// this binary was installed by the cask (brew links it from the Caskroom),
// else the release page.
func upgradeHint(exe, url string) string {
	if strings.Contains(filepath.ToSlash(exe), "/Caskroom/rootout/") {
		return brewUpgrade
	}
	return url
}
