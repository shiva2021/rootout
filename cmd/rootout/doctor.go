package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/ext"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/keychain"
	"github.com/shiva2021/rootout/internal/license"
	"github.com/shiva2021/rootout/internal/llm/ollama"
	"github.com/shiva2021/rootout/internal/output"
	"github.com/shiva2021/rootout/internal/run"
	"github.com/shiva2021/rootout/internal/tcc"
	"github.com/shiva2021/rootout/internal/trash"
)

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
the config file, this Mac's membership, stored API keys and Ollama with this Mac's chip and RAM.

Each line is a check mark or a "!" with a fix. Doctor only reads; it changes
nothing and installs nothing.

--json prints [{name, ok, detail, fix}].`,
		Example: `  rootout doctor
  rootout doctor --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := runChecks(cmd.Context())
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

	env, err := realEnv()
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

	if err == nil {
		add(membershipCheck(ctx, env))
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

// membershipCheck reports the installed key without contacting the key
// service. Grace, or any warning, is a "!" pointing at rootout license.
func membershipCheck(ctx context.Context, env fsx.Env) check {
	st := offlineState(ctx, env)
	c := check{Name: "Membership", Detail: st.Summary()}
	c.OK = (st.Allowed && st.Status != license.StatusGrace && st.Warning == "") || st.Status == license.StatusNone
	if st.Status == license.StatusNone {
		c.Detail = "no key · 7-day trial available (rootout license)"
	}
	if w := warnLine(st); w != "" {
		c.Detail += " · " + w
	}
	if !c.OK {
		c.Fix = "rootout license"
	}
	return c
}
