# empirio

Start Claude Code, Codex CLI, Gemini CLI and other coding agents on
[EmpirioLabs AI](https://empiriolabs.ai) models with one command. Your agents'
own settings stay as they are: empirio connects each session when it starts.

```sh
empirio                 # pick an agent and a model
empirio claude          # start Claude Code
empirio codex -m <model>
```

## Install

macOS and Linux:

```sh
curl -fsSL https://empiriolabs.ai/install.sh | sh
```

Windows (PowerShell):

```powershell
irm https://empiriolabs.ai/install.ps1 | iex
```

The installers check every download against the release's signed `SHA256SUMS`
([verifying a release](SECURITY.md#verifying-a-release)). You can
also download an archive for your platform from
[Releases](https://github.com/EmpirioLabs-AI/cli/releases) and put `empirio` on
your PATH, or build from source with Go: `go install github.com/EmpirioLabs-AI/cli/cmd/empirio@latest`.

## Sign in

```sh
empirio login
```

This opens your browser to approve the connection. The computer then appears
under **Settings > Connected applications** on the
[platform](https://platform.empiriolabs.ai), where you can disconnect it at any
time. Signing in on a computer without a browser, such as over SSH:
`empirio login --no-browser`. To use an API key instead, run
`empirio login --key`, or set `EMPIRIOLABS_API_KEY`, which always takes
precedence.

## Agents

<!-- agents:start (generated from the agent list; run `UPDATE_README=1 go test ./internal/app`) -->
| Agent | Command | Install |
|---|---|---|
| Claude Code | `empirio claude` | `curl -fsSL https://claude.ai/install.sh \| bash`<br>Windows: `irm https://claude.ai/install.ps1 \| iex` |
| Codex CLI | `empirio codex` | `npm install -g @openai/codex`<br>macOS: `brew install --cask codex` |
| OpenCode | `empirio opencode` | `curl -fsSL https://opencode.ai/install \| bash`<br>Windows: `npm install -g opencode-ai` |
| Gemini CLI | `empirio gemini` | `npm install -g @google/gemini-cli` |
| Qwen Code | `empirio qwen` | `npm install -g @qwen-code/qwen-code` |
| Crush | `empirio crush` | `npm install -g @charmland/crush`<br>macOS: `brew install charmbracelet/tap/crush`<br>Windows: `winget install charmbracelet.crush` |
| Kilo CLI | `empirio kilo` | `npm install -g @kilocode/cli` |
| Aider | `empirio aider` | `curl -LsSf https://aider.chat/install.sh \| sh`<br>Windows: `powershell -ExecutionPolicy ByPass -c "irm https://aider.chat/install.ps1 \| iex"` |
| goose | `empirio goose` | `curl -fsSL https://github.com/aaif-goose/goose/releases/download/stable/download_cli.sh \| bash`<br>Windows: `Invoke-WebRequest -Uri "https://raw.githubusercontent.com/aaif-goose/goose/main/download_cli.ps1" -OutFile download_cli.ps1; .\download_cli.ps1` |
| OpenHands CLI | `empirio openhands` | `uv tool install openhands --python 3.12` |
| Factory Droid | `empirio droid` | `curl -fsSL https://app.factory.ai/cli \| sh`<br>Windows: `irm https://app.factory.ai/cli/windows \| iex` |
<!-- agents:end -->

Arguments after the agent's name go to the agent, so `empirio claude -p "explain
this repo"` and `empirio codex exec "add tests"` work as they do without empirio.
`empirio agents --json` prints this list.

## Models

The model list comes from the live EmpirioLabs AI catalog, so new models appear
without an update. Each agent starts on your default model, or the catalog's
when you have not chosen one, and shows only the models it can run.

```sh
empirio models                    # every model with its context and prices
empirio models --agent codex      # the models Codex CLI can run
empirio model <model>             # make a model your default
empirio claude -m <model>         # one session on another model
```

Agents with a model for quick background work (titles, summaries) get the
lowest-cost featured model by the same author; `--fast-model <id>` picks another.

## Commands

| Command | |
|---|---|
| `empirio` | Choose an agent and a model, then start it |
| `empirio <agent> [-m <model>] [args]` | Start an agent; other arguments go to the agent |
| `empirio <agent> --print-env` | Show what the agent would receive, with the key masked |
| `empirio login` / `logout` | Sign in or out; `logout` also disconnects a browser sign-in |
| `empirio models` / `model` | List models, show or set your default |
| `empirio doctor` | Check your sign-in, the API and your agents |
| `empirio agents [--json]` | List the agents empirio can start |
| `empirio update` | Update empirio |
| `empirio uninstall` | Remove empirio and its saved settings |

## Your settings

empirio passes the connection to an agent for that session only. It does not edit
`~/.claude`, `~/.codex`, `~/.gemini` or any other agent's settings. Where an agent
saves a setting of its own during a session, empirio puts back the value it found
when the session ends. empirio keeps its own settings and your key in
`%APPDATA%\empirio` on Windows and `~/.config/empirio` elsewhere
(`EMPIRIO_CONFIG_DIR` to change it).

See [SECURITY.md](SECURITY.md) for how the key is handled and how to verify a
release.

## Documentation

[docs.empiriolabs.ai/cli](https://docs.empiriolabs.ai/cli)

## License

[MIT](LICENSE)
