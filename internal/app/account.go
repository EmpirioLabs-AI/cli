package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/api"
	"github.com/EmpirioLabs-AI/cli/internal/config"
)

func loginUsage() error {
	return errors.New("usage: empirio login [--no-browser | --key | --stdin]")
}

// login signs in with the browser by default; --no-browser does it from
// another device. --key asks for a key from the API keys page instead, and
// --stdin (or a pipe) reads one, for scripts.
func login(args []string) error {
	mode, noBrowser := "browser", false
	for _, a := range args {
		switch a {
		case "--no-browser":
			noBrowser = true
		case "--key":
			mode = "key"
		case "--stdin":
			mode = "stdin"
		default:
			return loginUsage()
		}
	}
	if mode == "browser" && !isTerminal(os.Stdin) {
		mode = "stdin"
	}
	var (
		key    string
		source string
		err    error
	)
	switch mode {
	case "browser":
		key, err = signInWithBrowser(noBrowser)
		source = "browser"
	case "key":
		fmt.Fprintln(stderr, "Paste an EmpirioLabs AI API key. Create one at "+accent(keysURL))
		key, err = readSecret("API key: ")
		source = "key"
	default:
		key, err = readSecret("")
		source = "key"
	}
	if err != nil {
		return err
	}
	if key == "" {
		return errors.New("no key entered")
	}
	client := api.New(Version)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	balance, err := client.Account(ctx, key)
	if errors.Is(err, api.ErrInvalidKey) {
		return errors.New("the API key was not accepted; check it at " + keysURL)
	}
	if err != nil {
		return fmt.Errorf("could not check the key with %s: %w", client.BaseURL, err)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	previous := cfg
	cfg.APIKey, cfg.KeySource = key, source
	if err := config.Save(cfg); err != nil {
		return err
	}
	// Signing in again replaces this computer's previous connection.
	if previous.KeySource == "browser" && previous.APIKey != "" && previous.APIKey != key {
		_ = revokeConnection(previous.APIKey)
	}
	if source == "browser" {
		okf("Signed in. Balance %s.", money(balance.Amount))
		notef("This computer appears under Settings > Connected applications, where you can disconnect it.")
	} else {
		okf("Signed in with %s. Balance %s.", config.Mask(key), money(balance.Amount))
	}
	notef("Saved to %s", config.Path())
	if env := strings.TrimSpace(os.Getenv(config.KeyEnv)); env != "" && env != key {
		warnf("%s is set in this shell and is used instead of the saved key", config.KeyEnv)
	}
	return nil
}

func logout() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if cfg.APIKey == "" {
		notef("No key is saved.")
		return nil
	}
	if cfg.KeySource == "browser" {
		if err := revokeConnection(cfg.APIKey); err != nil {
			warnf("Could not disconnect this computer from your account (%v); remove it under Settings > Connected applications.", err)
		} else {
			okf("Disconnected this computer from your account.")
		}
	}
	cfg.APIKey, cfg.KeySource = "", ""
	if err := config.Save(cfg); err != nil {
		return err
	}
	okf("Removed the saved key from %s", config.Path())
	if os.Getenv(config.KeyEnv) != "" {
		notef("%s is still set in this shell.", config.KeyEnv)
	}
	return nil
}
