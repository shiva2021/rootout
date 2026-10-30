// Command rootout-keys is the maintainer's offline tool for rootout licence
// keys: it makes the signing keypair, issues admin and test keys, and
// inspects keys. It is not shipped in releases.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/shiva2021/rootout/internal/license"
)

// Exit codes match the main binary (see docs/kb/conventions.md).
const (
	exitOK       = 0
	exitError    = 1
	exitBadInput = 2
)

// defaultKeyPath is set in main from the real home; run never reads the
// home directory, so tests always pass --out or --key.
var defaultKeyPath string

func main() {
	if home, err := os.UserHomeDir(); err == nil {
		defaultKeyPath = filepath.Join(home, ".config", "rootout-keys", "private.pem")
	}
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// badInput marks an error as the caller's mistake (exit 2).
type badInput struct{ err error }

func (e badInput) Error() string { return e.err.Error() }
func (e badInput) Unwrap() error { return e.err }

func run(args []string, in io.Reader, out, errw io.Writer) int {
	root := &cobra.Command{
		Use:   "rootout-keys",
		Short: "Make and inspect rootout licence keys (maintainer tool)",
		Long: `rootout-keys manages the Ed25519 signing key behind rootout licence keys.

The private key stays on the maintainer's Mac (default
~/.config/rootout-keys/private.pem, mode 0600). Back it up in the password
manager; see docs/kb/membership.md.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return badInput{fmt.Errorf("unknown command %q", args[0])}
			}
			return cmd.Help()
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return badInput{err} })
	root.SetArgs(args)
	root.SetIn(in)
	root.SetOut(out)
	root.SetErr(errw)
	root.AddCommand(genCmd(), adminCmd(), signCmd(), inspectCmd())

	err := root.Execute()
	if err == nil {
		return exitOK
	}
	_, _ = fmt.Fprintln(errw, "rootout-keys:", err)
	var bi badInput
	if errors.As(err, &bi) {
		return exitBadInput
	}
	return exitError
}

// requireFlags fails with a usage error unless every named flag was given.
func requireFlags(cmd *cobra.Command, names ...string) error {
	for _, n := range names {
		if !cmd.Flags().Changed(n) {
			return badInput{fmt.Errorf("required flag --%s not set", n)}
		}
	}
	return nil
}

// noArgs and exactArgs turn cobra's argument errors into usage errors.
func noArgs(cmd *cobra.Command, args []string) error {
	if err := cobra.NoArgs(cmd, args); err != nil {
		return badInput{err}
	}
	return nil
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := cobra.ExactArgs(n)(cmd, args); err != nil {
			return badInput{err}
		}
		return nil
	}
}

func genCmd() *cobra.Command {
	var keyPath string
	cmd := &cobra.Command{
		Use:   "gen",
		Short: "Generate the signing keypair (refuses to overwrite)",
		Long: `Generate a new Ed25519 keypair and write the private key as a PKCS#8 PEM
(file mode 0600, directory 0700). Prints the public key hex for
license.PublicKeyHex and the base64 PKCS#8 for the Worker's SIGNING_KEY secret.`,
		Example: `  rootout-keys gen
  rootout-keys gen --out /path/to/private.pem`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pub, priv, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				return fmt.Errorf("generate key: %w", err)
			}
			der, err := x509.MarshalPKCS8PrivateKey(priv)
			if err != nil {
				return fmt.Errorf("marshal key: %w", err)
			}
			if err := saveKey(keyPath, der); err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "public key hex: %s\nworker secret (PKCS8 base64): %s\n",
				hexOf(pub), base64.StdEncoding.EncodeToString(der))
			return err
		},
	}
	cmd.Flags().StringVar(&keyPath, "out", defaultKeyPath, "where to write the private key")
	return cmd
}

func adminCmd() *cobra.Command {
	var keyPath string
	var mid string
	cmd := &cobra.Command{
		Use:   "admin --mid <machine code>",
		Short: "Issue a non-expiring admin key for one Mac",
		Long: `Issue an admin key bound to one Mac. Get the machine code by running
"rootout license" on that Mac. Admin keys never expire.`,
		Example: `  rootout-keys admin --mid 0123456789abcdef0123456789abcdef`,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlags(cmd, "mid"); err != nil {
				return err
			}
			c := license.Claims{V: 1, Kind: license.KindAdmin, Sub: "admin", MID: mid, IAT: time.Now().Unix()}
			return issue(cmd.OutOrStdout(), c, keyPath)
		},
	}
	cmd.Flags().StringVar(&mid, "mid", "", "machine code from rootout license (32 lowercase hex)")
	cmd.Flags().StringVar(&keyPath, "key", defaultKeyPath, "private key file")

	return cmd
}

func signCmd() *cobra.Command {
	var keyPath string
	var kind, sub, mid string
	var days int
	cmd := &cobra.Command{
		Use:   "sign --kind patron|trial --sub <id> --mid <machine code> [--days N]",
		Short: "Issue a patron or trial key (for testing the service)",
		Long: `Issue a patron or trial key. --days defaults to the real lifetime (patron 35,
trial 7). A negative or zero --days is rejected because the key would
already be expired.`,
		Example: `  rootout-keys sign --kind patron --sub 12345 --mid 0123456789abcdef0123456789abcdef --days 35`,
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireFlags(cmd, "kind", "sub", "mid"); err != nil {
				return err
			}
			k := license.Kind(kind)
			if k != license.KindPatron && k != license.KindTrial {
				return badInput{fmt.Errorf("--kind must be patron or trial, got %q", kind)}
			}
			life := license.PatronLife
			if k == license.KindTrial {
				life = license.TrialLife
			}
			if cmd.Flags().Changed("days") {
				life = time.Duration(days) * 24 * time.Hour
			}
			now := time.Now()
			c := license.Claims{V: 1, Kind: k, Sub: sub, MID: mid, IAT: now.Unix(), EXP: now.Add(life).Unix()}
			return issue(cmd.OutOrStdout(), c, keyPath)
		},
	}
	cmd.Flags().StringVar(&kind, "kind", "", "patron or trial")
	cmd.Flags().StringVar(&sub, "sub", "", "subject, such as a Patreon user id")
	cmd.Flags().StringVar(&mid, "mid", "", "machine code from rootout license (32 lowercase hex)")
	cmd.Flags().IntVar(&days, "days", 0, "lifetime in days (default: the kind's real lifetime)")
	cmd.Flags().StringVar(&keyPath, "key", defaultKeyPath, "private key file")

	return cmd
}

func inspectCmd() *cobra.Command {
	var keyPath string
	cmd := &cobra.Command{
		Use:   "inspect <key>",
		Short: "Verify a key and print its claims as JSON",
		Long: `Verify a key against the public half of the private key file and print its
claims. Fails for a key signed by a different keypair.`,
		Example: `  rootout-keys inspect ro1.eyJ2IjoxLi4u.c2ln...`,
		Args:    exactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			priv, err := loadKey(keyPath)
			if err != nil {
				return err
			}
			c, err := license.Decode(args[0], priv.Public().(ed25519.PublicKey))
			if err != nil {
				return badInput{err}
			}
			b, err := json.MarshalIndent(c, "", "  ")
			if err != nil {
				return fmt.Errorf("encode claims: %w", err)
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return err
		},
	}
	cmd.Flags().StringVar(&keyPath, "key", defaultKeyPath, "private key file")
	return cmd
}

// issue validates c, signs it and prints the key.
func issue(out io.Writer, c license.Claims, keyPath string) error {
	if err := c.Valid(); err != nil {
		return badInput{err}
	}
	priv, err := loadKey(keyPath)
	if err != nil {
		return err
	}
	key, err := license.Encode(c, priv)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, key)
	return err
}

func hexOf(pub ed25519.PublicKey) string { return hex.EncodeToString(pub) }

// saveKey writes der as a PKCS#8 PEM, never replacing an existing file.
func saveKey(path string, der []byte) error {
	if path == "" {
		return badInput{errors.New("no key path: pass --out")}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create key directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%s already exists; refusing to overwrite", path)
	}
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// loadKey reads a PKCS#8 PEM Ed25519 private key.
func loadKey(path string) (ed25519.PrivateKey, error) {
	if path == "" {
		return nil, badInput{errors.New("no key path: pass --key")}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, fmt.Errorf("%s is not a PKCS#8 PEM private key", path)
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("%s is not an Ed25519 key", path)
	}
	return priv, nil
}
