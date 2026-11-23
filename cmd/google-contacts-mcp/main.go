// Command google-contacts-mcp is a remote MCP server exposing Google Contacts.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/samber/google-contacts-mcp/internal/config"
	"github.com/samber/google-contacts-mcp/internal/crypto"
	"github.com/samber/google-contacts-mcp/internal/google"
	"github.com/samber/google-contacts-mcp/internal/store"
)

// version is set at build time with -ldflags "-X main.version=<tag>".
var version = "dev"

const (
	cmdGenkey  = "genkey"
	cmdVersion = "version"

	// exitFailure is the conventional non-zero exit code for any error.
	exitFailure = 1
	// reasonCLI is the "user disconnected" log reason for the disconnect command.
	reasonCLI = "cli"
)

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr))
}

// run dispatches the subcommand and returns the process exit code. A missing
// subcommand, or one starting with "-", means serve with those flags.
func run(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	cmd := config.CmdServe
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}

	switch cmd {
	case cmdVersion:
		return runVersion(stdout)
	case cmdGenkey:
		return runGenkey(cmd, args, stdout, stderr)
	case config.CmdServe, config.CmdRekey, config.CmdDisconnect:
		return runConfigured(cmd, args, getenv, stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unknown command %q (want %s, %s, %s, %s or %s)\n",
			cmd, config.CmdServe, cmdGenkey, config.CmdRekey, config.CmdDisconnect, cmdVersion)

		return exitFailure
	}
}

// runVersion prints the build version.
func runVersion(stdout io.Writer) int {
	if _, err := fmt.Fprintln(stdout, version); err != nil {
		return exitFailure
	}

	return 0
}

// runGenkey prints a fresh keyring entry. It never loads config: it creates
// the key the config needs.
func runGenkey(cmd string, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintf(stderr, "%s: unexpected arguments %q\n", cmd, args)
		return exitFailure
	}

	entry, err := crypto.GenerateKey()
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitFailure
	}

	if _, err := fmt.Fprintln(stdout, entry); err != nil {
		return exitFailure
	}

	return 0
}

// runConfigured loads the configuration of a command that needs one, then runs it.
func runConfigured(cmd string, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	cfg, err := config.Load(cmd, args, getenv)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			config.Usage(stdout)
			return 0
		}

		_, _ = fmt.Fprintln(stderr, err)

		return exitFailure
	}

	switch cmd {
	case config.CmdRekey:
		err = runRekey(context.Background(), cfg, stdout)
	case config.CmdDisconnect:
		err = runDisconnect(context.Background(), cfg, stdout)
	default:
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		err = runServe(ctx, cfg, stdout)

		stop()
	}

	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return exitFailure
	}

	return 0
}

// openStore parses the keyring and opens the database named by cfg.
func openStore(ctx context.Context, cfg config.Config) (*store.Store, error) {
	kr, err := crypto.ParseKeyring(cfg.EncryptionKeys)
	if err != nil {
		return nil, err
	}

	return store.Open(ctx, cfg.DBPath, kr)
}

// runRekey re-encrypts every stored secret under the active key.
func runRekey(ctx context.Context, cfg config.Config, stdout io.Writer) (err error) {
	st, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()

	n, err := st.Rekey(ctx)
	if err != nil {
		return err
	}

	_, err = fmt.Fprintf(stdout, "re-encrypted %d rows\n", n)

	return err
}

// runDisconnect revokes a user's Google grant and deletes the user, found by
// e-mail or Google sub, with every token stored for them.
func runDisconnect(ctx context.Context, cfg config.Config, stdout io.Writer) (err error) {
	st, err := openStore(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, st.Close()) }()

	up := google.NewUpstream(google.UpstreamConfig{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
	}, st, nil)

	return disconnectUser(ctx, st, up, cfg.Args[0], stdout)
}

// revoker revokes a Google token.
type revoker interface {
	Revoke(ctx context.Context, token string) error
}

// disconnectUser revokes the user's refresh token at Google, then deletes the
// user. A failed revoke only warns: the local data must go regardless, and
// the user can still revoke from their Google account page.
func disconnectUser(ctx context.Context, st *store.Store, rv revoker, who string, stdout io.Writer) error {
	u, err := st.UserBySubOrEmail(ctx, who)
	if err != nil {
		return fmt.Errorf("disconnect %q: %w", who, err)
	}

	tok, err := st.GoogleToken(ctx, u.ID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		slog.InfoContext(ctx, "no google token to revoke", "user_id", u.ID)
	case err != nil:
		return fmt.Errorf("disconnect %q: %w", who, err)
	default:
		if err := rv.Revoke(ctx, tok.RefreshToken); err != nil {
			slog.WarnContext(ctx, "google revoke failed, deleting user anyway", "user_id", u.ID, "error", err)
		}
	}

	if err := st.DeleteUser(ctx, u.ID); err != nil {
		return fmt.Errorf("disconnect %q: %w", who, err)
	}

	slog.InfoContext(ctx, "user disconnected", slog.String("user_id", u.ID), slog.String("reason", reasonCLI))
	_, err = fmt.Fprintf(stdout, "disconnected %s\n", u.ID)

	return err
}
