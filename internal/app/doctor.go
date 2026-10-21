package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
	"github.com/EmpirioLabs-AI/cli/internal/api"
	"github.com/EmpirioLabs-AI/cli/internal/catalog"
	"github.com/EmpirioLabs-AI/cli/internal/config"
)

// agentVersion runs `<binary> --version` briefly and returns its first line.
func agentVersion(binary string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd, err := agentCommand(ctx, binary, []string{"--version"})
	if err != nil {
		return ""
	}
	cmd.Stdout, cmd.Stderr = &out, &out
	if cmd.Run() != nil && out.Len() == 0 {
		return ""
	}
	line := strings.TrimSpace(strings.SplitN(out.String(), "\n", 2)[0])
	if len(line) > 60 {
		line = line[:60]
	}
	return line
}

func doctor() (int, error) {
	problems := 0
	client := api.New(Version)

	fmt.Fprintln(stdout, bold("empirio "+Version))
	if latest, err := latestRelease(3 * time.Second); err == nil && newer(latest, Version) {
		warnf("empirio %s is available: run `empirio update`", latest)
	}

	key, source, err := config.Key()
	switch {
	case errors.Is(err, config.ErrNoKey):
		failf("No API key: run `empirio login`")
		problems++
	case err != nil:
		failf("%s", err)
		problems++
	default:
		where := guardHome(source)
		if source == config.KeyEnv {
			where = "the " + config.KeyEnv + " environment variable"
		}
		okf("API key %s, from %s", config.Mask(key), where)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		balance, err := client.Account(ctx, key)
		cancel()
		switch {
		case errors.Is(err, api.ErrInvalidKey):
			failf("%s does not accept this key; create a new one at %s", client.BaseURL, keysURL)
			problems++
		case err != nil:
			failf("Could not reach %s: %v", client.BaseURL, err)
			problems++
		case balance.Amount <= 0:
			warnf("%s accepts the key, but the balance is %s; add credits at https://platform.empiriolabs.ai/dashboard/billing", client.BaseURL, money(balance.Amount))
		default:
			okf("%s accepts the key. Balance %s.", client.BaseURL, money(balance.Amount))
		}
	}

	cat, err := loadCatalog(client, true)
	if err != nil {
		failf("%s", err)
		problems++
	} else {
		okf("Model list: %d models, %d for coding agents (read %s)", len(cat.Models), len(cat.For(catalog.Chat)), ago(cat.FetchedAt))
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, bold("Agents"))
	list := agents.All()
	type found struct{ path, version string }
	results := make([]found, len(list))
	var wg sync.WaitGroup
	for i, a := range list {
		results[i].path = a.Find()
		if results[i].path == "" {
			continue
		}
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i].version = agentVersion(results[i].path)
		}(i)
	}
	wg.Wait()
	installed := 0
	for i, a := range list {
		r := results[i]
		if r.path == "" {
			fmt.Fprintf(stdout, "  %s %-14s %s\n", dim("-"), a.Title, dim("not installed: "+a.InstallHint()))
			continue
		}
		installed++
		version := ""
		if r.version != "" {
			version = dim("(" + r.version + ")")
		}
		fmt.Fprintf(stdout, "  %s %-14s %s %s\n", success("✓"), a.Title, guardHome(r.path), version)
	}
	if installed == 0 {
		warnf("No supported agent is installed; the list above shows how to install each one.")
	}
	if self, err := os.Executable(); err == nil {
		if other := otherOnPath("empirio", self); other != "" {
			warnf("Another empirio is earlier on your PATH: %s", other)
		}
	}
	if problems > 0 {
		return 1, nil
	}
	return 0, nil
}

func guardHome(path string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(path, home) {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

// otherOnPath finds a different executable with the same name earlier on PATH.
func otherOnPath(name, self string) string {
	selfReal, _ := filepath.EvalSymlinks(self)
	p, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	real, _ := filepath.EvalSymlinks(p)
	if strings.EqualFold(filepath.Clean(real), filepath.Clean(selfReal)) {
		return ""
	}
	return p
}
