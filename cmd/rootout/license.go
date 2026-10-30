package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/fsx"
	"github.com/shiva2021/rootout/internal/license"
	"github.com/shiva2021/rootout/internal/output"
)

func newLicenseCmd(g *globalFlags) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "license [key]",
		Short: "Show this Mac's membership, install a licence key or start the trial",
		Long: `Deletion (remove, orphans, uninstalling extensions) and AI assessment need a
membership after a free 7-day trial. Listing apps, dry runs and heuristic
extension scans are always free.

With no argument, show this Mac's status and its machine code, the link
where a member gets the key for this Mac and, when no key is installed,
offer the trial (--yes starts it without asking). Starting the trial asks
the key service once; nothing else about this Mac is sent.

With a key, check it was issued for this Mac and install it. "license status"
shows the status without contacting the key service; "license remove"
deletes the installed key.

--json prints the status as JSON and never asks anything.`,
		Example: `  rootout license                 # status, machine code and where to get a key
  rootout license ro1.eyJ2Ijo…    # install the key for this Mac
  rootout license --yes           # start the 7-day trial without asking
  rootout license status --json   # status as JSON
  rootout license remove          # delete the installed key`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			env, err := realEnv()
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			if len(args) == 1 {
				out := io.Writer(os.Stdout)
				if g.json {
					out = io.Discard
				}
				if err := licenseInstall(ctx, env, args[0], out); err != nil || !g.json {
					return err
				}
				return licenseStatus(ctx, env, true, os.Stdout)
			}
			if g.json {
				return licenseStatus(ctx, env, true, os.Stdout)
			}
			return licenseShow(ctx, env, yes, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "start the trial without asking (when no key is installed)")
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show this Mac's membership without contacting the key service",
		Long: `Show this Mac's membership from the installed key alone: no network.
--json prints {status, allowed, kind, machine_id, expires, days_left, warning, reason}.`,
		Example: `  rootout license status
  rootout license status --json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			env, err := realEnv()
			if err != nil {
				return err
			}
			return licenseStatus(cmd.Context(), env, g.json, os.Stdout)
		},
	}, &cobra.Command{
		Use:   "remove",
		Short: "Delete the installed licence key",
		Long: `Delete the installed licence key from this Mac. A member can install it
again at any time; a used trial cannot be restarted.`,
		Example: `  rootout license remove`,
		Args:    cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			env, err := realEnv()
			if err != nil {
				return err
			}
			return licenseRemove(env, g.json, os.Stdout)
		},
	})
	return cmd
}

// licenseInstall checks key was issued for this Mac and is current, saves it
// and prints the resulting status. A malformed, expired or future-dated key,
// or one for another Mac, is exit 2.
func licenseInstall(ctx context.Context, env fsx.Env, key string, out io.Writer) error {
	d := offlineDeps(env)
	pub := d.Pub
	if pub == nil {
		var err error
		if pub, err = license.EmbeddedPublicKey(); err != nil {
			return &exitErr{code: exitError, msg: "this build cannot verify licence keys (no public key embedded)"}
		}
	}
	key = strings.TrimSpace(key)
	c, err := license.Decode(key, pub)
	if err != nil {
		why := strings.TrimPrefix(strings.TrimPrefix(err.Error(), "license: "), "invalid key: ")
		return &exitErr{code: exitBadInput, msg: "that is not a valid licence key (" + why + ")"}
	}
	mid, err := license.MachineID(ctx, d.Run)
	if err != nil {
		return fmt.Errorf("could not read this Mac's id: %w", err)
	}
	if c.MID != mid {
		return &exitErr{code: exitBadInput, msg: "this key was issued for another Mac; get the key for this Mac at " + keysURL(mid)}
	}
	now := time.Now()
	if d.Now != nil {
		now = d.Now()
	}
	if exp, ok := c.Expires(); ok && !now.Before(exp) {
		return &exitErr{code: exitBadInput, msg: "this key has expired; get a new key for this Mac at " + keysURL(mid)}
	}
	if c.IAT > now.Add(license.IATSkew).Unix() {
		return &exitErr{code: exitBadInput, msg: "this key is not valid yet; check your clock"}
	}
	if err := (license.Store{Path: license.Path(env, os.Getenv)}).Save(key); err != nil {
		return err
	}
	resetGate()
	st := license.Resolve(ctx, d)
	_, _ = fmt.Fprintln(out, st.Summary())
	if st.Warning != "" {
		_, _ = fmt.Fprintln(out, "warning:", st.Warning)
	}
	return nil
}

// licenseStatus prints this Mac's membership from the installed key alone.
func licenseStatus(ctx context.Context, env fsx.Env, jsonOut bool, out io.Writer) error {
	st := offlineState(ctx, env)
	if jsonOut {
		return output.New(out, true).Emit(st)
	}
	printState(out, st)
	return nil
}

// licenseShow is bare `rootout license`: the status, the machine code, where
// to get a key and, without a key, the trial offer.
func licenseShow(ctx context.Context, env fsx.Env, yes bool, in io.Reader, out io.Writer) error {
	st := offlineState(ctx, env)
	printState(out, st)
	if st.MID != "" {
		_, _ = fmt.Fprintln(out, "get a key:", keysURL(st.MID))
	}
	if st.Status != license.StatusNone {
		return nil
	}
	if !yes && !confirmTrial(in, out) {
		return nil
	}
	st, err := gate(ctx, env, true)
	if err != nil {
		return denyExit(err)
	}
	trialNotice(st, out)
	return nil
}

// licenseRemove deletes the installed key.
func licenseRemove(env fsx.Env, jsonOut bool, out io.Writer) error {
	s := license.Store{Path: license.Path(env, os.Getenv)}
	_, err := s.Load()
	had := !errors.Is(err, license.ErrNoKey)
	if had {
		if err := s.Remove(); err != nil {
			return err
		}
	}
	resetGate()
	if jsonOut {
		return output.New(out, true).Emit(map[string]bool{"removed": had})
	}
	msg := "licence key removed"
	if !had {
		msg = "no licence key installed"
	}
	_, _ = fmt.Fprintln(out, msg)
	return nil
}

func printState(out io.Writer, st license.State) {
	_, _ = fmt.Fprintln(out, st.Summary())
	if st.Warning != "" {
		_, _ = fmt.Fprintln(out, "warning:", st.Warning)
	}
	if st.MID != "" {
		_, _ = fmt.Fprintln(out, "machine code:", st.MID)
	}
}
