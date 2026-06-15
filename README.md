<h1 align="center">pssh</h1>
<p align="center"><b>Passbolt-aware SSH launcher.</b> Pick a host, and pssh resolves its
Passbolt password and logs you in — never typed, never on the clipboard.</p>

<p align="center"><img src="demo/demo.svg" alt="pssh demo" width="760"></p>

---

`pssh` replaces `ssh` for interactive logins to password-auth hosts. It reads your
SSH config, finds the Passbolt resource linked to the host you pick, fetches the
password, and feeds it straight to `ssh` via `SSH_ASKPASS` — so the secret is
**never shown, copied, stored, or left in your shell history**. The first time you
connect to an unlinked host it lets you pick a resource and remembers it.

## Features

- **Host picker** over your `~/.ssh/config` (with `Include` expansion) via a `gum`
  fuzzy filter — or pass the host directly.
- **Automatic auth** — the password is delivered to `ssh` through `SSH_ASKPASS`;
  no paste step, no `sshpass`, no secret in `ps`/argv/history.
- **Clipboard fallback** for older `ssh` — UTF-16LE-safe for WSL's `clip.exe`.
- **Two mapping stores** — an `# pssh:` comment in your ssh config, or the Passbolt
  resource `URI`. URI inference is used only when unambiguous; otherwise pssh asks.
- **Plugins** — `pssh <name> <host>` runs `pssh-<name>` with the password wired up
  (e.g. an `ssh-lazysql`-style DB tunnel), no plugin-side code needed.
- **`pssh doctor`** — checks dependencies and Passbolt connectivity, and guides you
  through fixing whatever's wrong.

## Requirements

| Tool | Required? | For |
|------|-----------|-----|
| `ssh` (OpenSSH ≥ 8.4) | yes | the connection + askpass delivery |
| [`passbolt`](https://github.com/passbolt/go-passbolt-cli) CLI | yes | fetching secrets (configure it once with `passbolt configure`) |
| [`gum`](https://github.com/charmbracelet/gum) | optional | nicer pickers (falls back to a numbered prompt) |
| a clipboard tool | optional | only for the `clipboard` delivery mode |

## Install

```sh
# from a clone
git clone https://github.com/jgsqware/pssh && cd pssh
mise run setup        # builds pssh into ~/.local/bin AND installs bundled plugins
# or just the binary:
go build -o ~/.local/bin/pssh .       # or: mise run install

# or with go install (private repo)
GOPRIVATE=github.com/jgsqware/* go install github.com/jgsqware/pssh@latest
```

## Usage

```text
pssh                  pick a host, then connect
pssh <hostname>       connect straight to a host
pssh link <host>      (re)assign the Passbolt resource for a host
pssh unlink <host>    remove a host's association
pssh <name> <host>    run plugin pssh-<name> with the host's password
pssh plugins          list available plugins
pssh doctor           check dependencies + passbolt connectivity
pssh --verbose ...    trace resolution + delivery (never prints the secret)
```

```sh
pssh                       # fuzzy-pick a host and connect
pssh app-prod-1       # connect to a specific host
pssh link app-prod-1  # pick the Passbolt resource to associate
```

## Configuration

Set via environment or `~/.config/pssh/config` (`KEY=value` lines):

| Variable | Default | Meaning |
|----------|---------|---------|
| `PSSH_DELIVER` | `auto` | `auto` \| `askpass` \| `clipboard` |
| `PSSH_STORE` | `ssh-comment` | `ssh-comment` \| `passbolt-uri` |
| `PSSH_CLEAR` | _(off)_ | clipboard auto-clear seconds (clipboard mode) |
| `PSSH_CLIPBOARD` | _(auto)_ | explicit clipboard command override |
| `PSSH_PASSBOLT_FLAGS` | _(none)_ | extra flags for every `passbolt` call |
| `PSSH_PLUGIN_DIR` | `~/.config/pssh/plugins` | where `pssh-<name>` plugins live |

## How it works

1. **Resolve** the host → a Passbolt resource id: first an `# pssh:` comment in your
   ssh config (offline), then a URI match (inferring the real `HostName` via
   `ssh -G`). Zero or multiple URI matches defer to the resource picker.
2. **Fetch** the password (one authenticated `passbolt` session per run).
3. **Deliver** it: pssh re-execs *itself* as the `SSH_ASKPASS` helper and
   `exec`s the real `ssh`, which calls back for the password. Your full ssh config
   (ProxyJump, ControlMaster, agent) still applies.

## Plugins

`pssh <name> <host>` runs an executable named `pssh-<name>` (from
`~/.config/pssh/plugins` or `PATH`) with the host's password wired up via the
askpass env — the plugin's own `ssh` calls authenticate automatically, with no
plugin-side code.

This repo bundles one in [`plugins/`](plugins/):

| Plugin | What `pssh <name> <host>` does |
|--------|--------------------------------|
| **`pg`** | pick a remote Docker container, extract its DB connection string, tunnel it locally, and open [`lazysql`](https://github.com/jorgerojas26/lazysql). Needs `gum`, `jq`, `lazysql`. |

Install the bundled plugins (symlinks them into the plugin dir):

```sh
mise run install-plugins        # or `mise run setup` for binary + plugins
pssh pg app-prod-1          # pick a container, tunnel its DB, open lazysql
pssh pg -t app-prod-1 5544  # tunnel-only on an explicit local port
```

<p align="center"><img src="demo/pg.svg" alt="pssh pg demo" width="760"></p>

Add your own: drop a `pssh-<name>` executable in `~/.config/pssh/plugins`.

## Development

```sh
mise run check         # fmt + vet + lint + test
mise run test-docker   # dockerized install + end-to-end askpass login test
```

See [`test/`](test/) for the container test (real sshd + a stubbed passbolt — no
VPN needed). The original POSIX shell implementation is preserved as `pssh.sh`.
