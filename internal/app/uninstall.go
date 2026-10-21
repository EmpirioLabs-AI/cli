package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EmpirioLabs-AI/cli/internal/config"
)

func uninstall(args []string) error {
	yes := false
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			yes = true
		default:
			return fmt.Errorf("unknown flag %q (usage: empirio uninstall [--yes])", a)
		}
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}
	dir := config.Dir()
	fmt.Fprintln(stderr, "This removes:")
	fmt.Fprintf(stderr, "  %s  (the empirio program)\n", guardHome(self))
	fmt.Fprintf(stderr, "  %s  (your saved key, settings and model list)\n", guardHome(dir))
	fmt.Fprintln(stderr, "Your agents and their own settings are not touched; empirio never changed them.")
	if !yes {
		if !isTerminal(os.Stdin) {
			return errors.New("add --yes to confirm when not running in a terminal")
		}
		if !confirm("Remove empirio?") {
			notef("Nothing was removed.")
			return nil
		}
	}
	// A browser sign-in is a connection on the account; end it with the key.
	if cfg, err := config.Load(); err == nil && cfg.KeySource == "browser" && cfg.APIKey != "" {
		if err := revokeConnection(cfg.APIKey); err != nil {
			warnf("Could not disconnect this computer from your account (%v); remove it under Settings > Connected applications.", err)
		} else {
			okf("Disconnected this computer from your account.")
		}
	}
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("could not remove %s: %w", dir, err)
	}
	if err := removeSelf(self); err != nil {
		return fmt.Errorf("could not remove %s: %w (delete it by hand)", self, err)
	}
	removeFromPath(filepath.Dir(self))
	okf("empirio is removed.")
	if os.Getenv(config.KeyEnv) != "" {
		notef("%s is still set in your environment; empirio never set it.", config.KeyEnv)
	}
	return nil
}
