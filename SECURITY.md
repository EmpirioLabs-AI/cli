# Security

## Reporting a vulnerability

Report a vulnerability privately through
[GitHub security advisories](https://github.com/EmpirioLabs-AI/cli/security/advisories/new)
or by email to support@empiriolabs.ai. Please do not open a public issue.

## How empirio handles your key

- The key is saved in `config.json` in empirio's own directory (`%APPDATA%\empirio`
  on Windows, `~/.config/empirio` elsewhere), readable only by you.
  `EMPIRIOLABS_API_KEY` in the environment is used instead when it is set.
- An agent receives the key in its environment or in a session file that only you
  can read and that empirio removes when the session ends. The key never appears
  on a command line, so other users of the computer cannot read it from the
  process list.
- `empirio <agent> --print-env` shows what an agent would receive, with the key
  masked.
- empirio does not change an agent's own settings. Where an agent saves a setting
  of its own during a session, empirio puts back the value it found when the
  session ends.
- Signing in with the browser uses OAuth 2.1 with PKCE and a loopback redirect.
  The connection appears under **Settings > Connected applications**, where you can
  disconnect it; `empirio logout` and `empirio uninstall` disconnect it too.

## Verifying a release

Every release publishes `SHA256SUMS`, its signature `SHA256SUMS.sig`, and a build
provenance attestation for every file. `empirio update` installs nothing unless
`SHA256SUMS` is signed by the release key in
[`internal/release/allowed_signers`](internal/release/allowed_signers) and the
download matches its checksum. The installers check the same signature with
`ssh-keygen` where it is available (macOS, most Linux systems, Windows 10 and later).

To check a download yourself, with `allowed_signers` from this repository:

```sh
ssh-keygen -Y verify -f allowed_signers -I release@empiriolabs.ai -n empirio-release -s SHA256SUMS.sig < SHA256SUMS
sha256sum --check --ignore-missing SHA256SUMS
gh attestation verify empirio_linux_amd64.tar.gz --repo EmpirioLabs-AI/cli
```
