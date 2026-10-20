package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/app"
	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/output"
)

func newListCmd(g *globalFlags) *cobra.Command {
	var (
		system bool
		search string
	)
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List installed applications",
		Long:  "List application bundles in /Applications and ~/Applications. System applications are hidden unless --system is given; they are never removable.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := fsx.Real()
			if err != nil {
				return err
			}
			apps, err := app.Discover(env)
			if err != nil {
				return err
			}
			if !system {
				apps = visible(apps)
			}
			if search != "" {
				matches := app.Fuzzy(apps, search)
				apps = apps[:0]
				for _, m := range matches {
					apps = append(apps, m.App)
				}
			}
			return printApps(output.New(os.Stdout, g.json), apps)
		},
	}
	cmd.Flags().BoolVar(&system, "system", false, "include system applications")
	cmd.Flags().StringVarP(&search, "search", "s", "", "fuzzy-filter by name or bundle id")
	return cmd
}

// visible drops protected (system) apps.
func visible(apps []app.App) []app.App {
	out := apps[:0:0]
	for _, a := range apps {
		if !a.Protected {
			out = append(out, a)
		}
	}
	return out
}

func printApps(w *output.Writer, apps []app.App) error {
	t := output.Table{
		Columns: []string{"name", "bundle id", "version", "size", "source"},
		Rows:    apps,
	}
	for _, a := range apps {
		t.Cells = append(t.Cells, []string{
			a.Name, a.BundleID, a.Version, output.Bytes(a.SizeBytes), source(a),
		})
	}
	if err := w.Print(t); err != nil {
		return err
	}
	w.Printf("\n%d applications\n", len(apps))
	return nil
}

func source(a app.App) string {
	switch {
	case a.Protected:
		return "system"
	case a.Cask != "":
		return fmt.Sprintf("brew cask %s", a.Cask)
	default:
		return ""
	}
}
