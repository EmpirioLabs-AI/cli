// Package app is the empirio command line.
package app

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/EmpirioLabs-AI/cli/internal/agents"
	"github.com/EmpirioLabs-AI/cli/internal/api"
	"github.com/EmpirioLabs-AI/cli/internal/config"
)

// Version is set at build time.
var Version = "0.0.0-dev"

const (
	keysURL = "https://platform.empiriolabs.ai/dashboard/api-keys"
	docsURL = "https://docs.empiriolabs.ai/integrations"
)

func usage() string {
	var b strings.Builder
	b.WriteString(bold("empirio") + " starts your coding agent on EmpirioLabs AI models, without changing its settings.\n\n")
	b.WriteString(bold("Usage") + "\n")
	b.WriteString("  empirio                         choose an agent and start it\n")
	b.WriteString("  empirio <agent> [flags] [args]  start an agent; other arguments go to the agent\n")
	b.WriteString("  empirio login [--no-browser]    sign in with your browser (--key pastes a key)\n")
	b.WriteString("  empirio models [--agent A]      list models with context and prices\n")
	b.WriteString("  empirio model [<id>]            show or set your default model\n")
	b.WriteString("  empirio doctor                  check your key, the API and your agents\n")
	b.WriteString("  empirio update                  update empirio\n")
	b.WriteString("  empirio agents [--json]         list the agents empirio can start\n")
	b.WriteString("  empirio logout | uninstall | version | help\n\n")
	b.WriteString(bold("Agents") + "\n")
	for _, a := range agents.All() {
		fmt.Fprintf(&b, "  %-10s %s\n", a.Name, a.Title)
	}
	b.WriteString("\n" + bold("Flags for an agent") + "\n")
	b.WriteString("  -m, --model <id>      run on this model (default: your default, else the catalog's)\n")
	b.WriteString("  --fast-model <id>     model for quick background work, where the agent has one\n")
	b.WriteString("  --print-env           show what would be passed to the agent, then exit\n\n")
	b.WriteString("Docs: " + docsURL + "\n")
	return b.String()
}

// Run executes the command line and returns the process exit code.
func Run(args []string) int {
	removeOldBinary()
	code, err := run(args)
	if err != nil {
		failf("%s", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

type launchFlags struct {
	model    string
	fast     string
	printEnv bool
}

// takeFlags consumes empirio's own flags from the front of args.
func takeFlags(args []string, f *launchFlags) ([]string, error) {
	for len(args) > 0 {
		a := args[0]
		switch {
		case a == "--print-env":
			f.printEnv = true
			args = args[1:]
		case a == "-m" || a == "--model" || a == "--fast-model":
			if len(args) < 2 || strings.HasPrefix(args[1], "-") {
				return nil, fmt.Errorf("%s needs a model id", a)
			}
			if a == "--fast-model" {
				f.fast = args[1]
			} else {
				f.model = args[1]
			}
			args = args[2:]
		case strings.HasPrefix(a, "--model="):
			f.model, args = strings.TrimPrefix(a, "--model="), args[1:]
		case strings.HasPrefix(a, "--fast-model="):
			f.fast, args = strings.TrimPrefix(a, "--fast-model="), args[1:]
		case a == "--":
			return args[1:], nil
		default:
			return args, nil
		}
	}
	return args, nil
}

func run(args []string) (int, error) {
	var flags launchFlags
	args, err := takeFlags(args, &flags)
	if err != nil {
		return 2, err
	}
	if len(args) == 0 {
		if flags.model == "" && !flags.printEnv && isTerminal(os.Stdin) && isTerminal(os.Stdout) {
			if err := checkOverrides(); err != nil {
				return 2, err
			}
			return menu()
		}
		fmt.Fprint(stdout, usage())
		return 0, nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "-h", "--help", "help":
		fmt.Fprint(stdout, usage())
		return 0, nil
	case "-v", "--version", "version":
		fmt.Fprintln(stdout, "empirio", Version)
		return 0, nil
	}
	if err := checkOverrides(); err != nil {
		return 2, err
	}
	switch cmd {
	case "login":
		return 0, login(rest)
	case "logout":
		return 0, logout()
	case "models":
		return 0, models(rest)
	case "model":
		return 0, modelCmd(rest)
	case "doctor":
		return doctor()
	case "agents":
		return 0, agentList(rest)
	case "update", "upgrade":
		return 0, update(rest)
	case "uninstall":
		return 0, uninstall(rest)
	}
	agent, ok := agents.Get(cmd)
	if !ok {
		return 2, fmt.Errorf("unknown command %q; run `empirio help` for the list", cmd)
	}
	rest, err = takeFlags(rest, &flags)
	if err != nil {
		return 2, err
	}
	return start(agent, flags, rest)
}

// checkOverrides refuses an environment override that would send the key or
// a download in the clear.
func checkOverrides() error {
	for _, name := range []string{"EMPIRIOLABS_BASE_URL", "EMPIRIO_OAUTH_ISSUER", "EMPIRIO_RELEASES"} {
		if err := api.CheckOverride(name); err != nil {
			return err
		}
	}
	return nil
}

// start launches one agent session.
func start(agent agents.Agent, flags launchFlags, args []string) (int, error) {
	client := api.New(Version)
	key, _, err := config.Key()
	if errors.Is(err, config.ErrNoKey) && isTerminal(os.Stdin) && !flags.printEnv {
		fmt.Fprintln(stderr, "Sign in to EmpirioLabs AI to continue.")
		if err := login(nil); err != nil {
			return 1, err
		}
		key, _, err = config.Key()
	}
	if err != nil {
		return 1, err
	}
	cat, err := loadCatalog(client, false)
	if err != nil {
		return 1, err
	}
	picked, cat, err := pickModels(client, cat, agent, flags.model, flags.fast)
	if err != nil {
		return 2, err
	}
	dir, err := runDir()
	if err != nil {
		return 1, err
	}
	sweepRunDir(dir)
	binary := agent.Find()
	if binary == "" && !flags.printEnv {
		return 127, fmt.Errorf("%s is not installed. Install it with:\n    %s\n  then run `empirio %s` again", agent.Title, agent.InstallHint(), agent.Name)
	}
	if binary == "" {
		binary = agent.Binaries[0]
	}
	plan, err := agent.Plan(agents.Context{
		Key:     key,
		BaseURL: client.BaseURL,
		Main:    picked.Main,
		Fast:    picked.Fast,
		Catalog: cat,
		Version: Version,
		RunDir:  dir,
	}, binary, args)
	if err != nil {
		return 2, err
	}
	if flags.printEnv {
		fmt.Fprint(stdout, render(plan))
		return 0, nil
	}
	for _, n := range picked.Notes {
		notef("%s", n)
	}
	checkForUpdate()
	okf("%s", plan.Summary)
	return launch(plan)
}
