# pssh — Passbolt-aware SSH launcher

## Implementation: Go (the shell script is legacy)
pssh is now a **Go program** (pure stdlib, no third-party deps). It execs the
real `ssh`, `gum`, and `passbolt` CLIs and parses the ssh config itself. The
original POSIX shell version is kept as `pssh.sh` for reference only.

- Build:   `go build -o bin/pssh .`   ·   Install: `go build -o ~/.local/bin/pssh .`
- Test:    `go test ./...`   ·   Vet: `go vet ./...`
- Layout:
  - `main.go` — entry; askpass mode + `__clipclear` helper + dispatch
  - `internal/config` — env + `~/.config/pssh/config` (KEY=VALUE) loader
  - `internal/sshcfg` — ssh config parse, Include expansion (depth-capped),
    comment store (atomic, symlink-preserving), `ssh -G` HostName inference
  - `internal/passbolt` — passbolt CLI wrapper; **caches the resource list** so a
    run authenticates at most twice (probe/list + get); error classification
  - `internal/clip` — clipboard detect + **native UTF-16LE** encode for clip.exe
    (no iconv dependency)
  - `internal/ui` — gum pickers + validated numbered fallback, styled output
  - `internal/app` — orchestration: resolve, link, connect, plugins, doctor,
    delivery (askpass/clipboard)
- Improvements over the shell version (from the code review): single Passbolt
  session via the list cache (kills the per-call re-auth flakiness); empty
  askpass prompt now declines (no misdelivery); validated numeric picker input;
  atomic symlink-preserving config writes; Include recursion guard; real error
  handling instead of `set -e` gotchas. The shell-only deps `jq` and `iconv` are
  gone (done natively in Go).

## Goal
A wrapper around `ssh` that lets you pick a host from your SSH config via a `gum`
fuzzy picker, resolves the Passbolt password linked to that host, copies it to
the clipboard, then launches a normal `ssh` session. The first time you connect
to a host with no link, pssh lets you pick a Passbolt resource and remembers the
host→resource association for next time.

Usage replaces `ssh` for interactive logins:
```
pssh                # pick a host, then connect
pssh <hostname>     # skip the host picker, go straight to it
pssh link <host>    # (re)assign the Passbolt resource for a host
pssh unlink <host>  # remove the association
pssh doctor         # check dependencies + passbolt connectivity
```

## Decisions (locked in)
- **Implementation:** single POSIX-ish **shell script** (`pssh`), no compile step.
  Target `/bin/sh`-compatible where reasonable; bashisms allowed if documented.
- **Password action (`PSSH_DELIVER`):** default **`auto`** →
  - **`askpass`** (OpenSSH >= 8.4): feed the password straight to ssh via
    `SSH_ASKPASS` + `SSH_ASKPASS_REQUIRE=force`. pssh re-execs *itself* as the
    askpass helper (guarded by `PSSH_ASKPASS=1`), reading the secret from an env
    var it exports only to ssh. The password is never on the clipboard, in argv,
    echoed, or in shell history; it is not forwarded to the remote. The helper
    answers only password/passphrase prompts and declines host-key prompts (so an
    unknown host still fails closed — accept its key once before using askpass).
  - **`clipboard`**: copy to clipboard (UTF-16LE for clip.exe), user pastes;
    `PSSH_CLEAR=<secs>` to auto-clear. Useful when you also need the secret for
    sudo on the remote. `auto` falls back to this when ssh is too old.
  pssh never prints the password to stdout.
- **Mapping store:** **configurable** between two backends (env `PSSH_STORE`):
  - `ssh-comment` — annotate each `Host` block with `# pssh: <resourceID>`.
  - `passbolt-uri` — match on the Passbolt resource whose `URI` equals the host.
  Default: `ssh-comment`. Both are read; the configured one is used for writes.
- **Clipboard auto-clear:** **off by default** (`PSSH_CLEAR` unset). Opt in by
  setting `PSSH_CLEAR=<seconds>`.
- **`passbolt-uri` matching:** **infer the real target**. Try `URI == <Host alias>`
  first, then fall back to the resolved `HostName`/IP from the host's ssh config
  block, so an alias that differs from the real hostname still matches.
- **Passbolt not connected:** pssh **detects and helps** (see Doctor / bootstrap).

## Environment (this machine)
- `gum` v0.17 — `gum filter` / `gum choose` for selection, `gum spin`, `gum style`.
- `passbolt` CLI v0.4.2 — key commands:
  - `passbolt list resource -j -c ID -c Name -c Username -c URI` → JSON list.
  - `passbolt get resource --id <id> -j` (includes `Password` column) → secret.
  - To force secret retrieval in list: add `-c Password` (JSON includes all fields).
- `~/.ssh/config` uses `Include config.d/*` — pssh must resolve Includes.
- Clipboard: only `clip.exe` (WSL2 → Windows clipboard) is present. Detect in
  order: `wl-copy`, `xclip -selection clipboard`, `xsel -b`, `pbcopy`, `clip.exe`.
- WSL2 / Linux 6.6, shell `/bin/sh`, no git repo yet.

## Flow
1. **Parse hosts.** Read `~/.ssh/config`, expand `Include` globs (relative to
   `~/.ssh/`), collect every `Host` pattern that isn't a wildcard (`*`, `?`).
   Keep, per host, any trailing `# pssh: <id>` comment found in its block.
2. **Pick host.** If no host arg: `gum filter` over the host list. With an arg,
   use it directly.
3. **Resolve resource id** for the host:
   - `ssh-comment` store → the `# pssh:` id parsed in step 1.
   - `passbolt-uri` store → `passbolt list resource -j --filter 'URI == "<host>"'`;
     if no match, retry against the resolved `HostName`/IP for that host.
4. **If no link exists → link flow:** `passbolt list resource -j` → feed
   `Name (Username — URI)` lines to `gum filter` → user picks one → persist the
   association via the configured store (write `# pssh:` comment into the right
   config.d file, or note that the resource URI should be the host).
5. **Fetch + copy.** `passbolt get resource --id <id> -j`, extract `Password`,
   pipe to the detected clipboard tool. Show a `gum style` confirmation (never the
   secret). Auto-clear is off unless `PSSH_CLEAR=<seconds>` is set.
6. **Connect.** `exec ssh <host> "$@"` so signals/TTY behave like real ssh.

## Passbolt connectivity / bootstrap
- Before any passbolt call, run a cheap probe (e.g. `passbolt list resource -j`
  with a short `--timeout`) inside `gum spin`. On failure:
  - No config / not configured → guide the user: print the exact
    `passbolt configure --serverAddress … --userPrivateKeyFile … && passbolt verify`
    steps, and offer to open `passbolt configure` interactively.
  - MFA / auth error → surface passbolt's message and suggest re-running
    `passbolt verify` or checking `--mfaMode`.
- `pssh doctor` runs all checks non-fatally: `gum`, `passbolt`, a clipboard tool,
  ssh config readable, Includes resolvable, passbolt reachable + authenticated.

## Plugins
- A plugin is any executable named `pssh-<name>` in `$PSSH_PLUGIN_DIR`
  (default `~/.config/pssh/plugins`) or on `PATH`.
- `pssh <name> <host> [args…]` resolves the host's Passbolt password (same
  resolve/link/fetch as connect), exports the askpass env, and `exec`s
  `pssh-<name>` with all args unchanged. The host is the first non-option arg;
  with no host given (`pssh <name>`) the host picker runs and the choice is
  prepended for the plugin.
- The plugin needs **no pssh-specific code**: its own `ssh` calls authenticate
  via the inherited `SSH_ASKPASS`. Plugins should call `ssh`, not `pssh`
  recursively (the askpass env would short-circuit a nested pssh).
- `pssh plugins` lists discovered plugins. A plugin name shadows a same-named host.
- **ssh-lazysql integration (as `pg`):** symlink it in as a plugin — no edits —
  `ln -s ~/.local/bin/ssh-lazysql ~/.config/pssh/plugins/pssh-pg`, then
  `pssh pg <host>` (or just `pssh pg` to pick a host first). ssh-lazysql
  authenticates once on its multiplexed master
  connection; askpass supplies the password there, and every later hop reuses the
  socket. clipboard fallback is used only when ssh is too old for askpass.

## Config & files
- `~/.config/pssh/config` (sourced shell file) for `PSSH_STORE`, `PSSH_CLEAR`,
  clipboard override, default passbolt flags.
- No secret is ever written to disk by pssh. Mappings (ids) are not secrets.

## Resolved
- Clipboard auto-clear: **off by default**, opt in via `PSSH_CLEAR=<seconds>`.
- `passbolt-uri` matching: **infer** — try the `Host` alias, then the resolved
  `HostName`/IP.

## Open questions to confirm before building
1. Passbolt resource **Name** display in the picker — show `Name`, `Username`,
   and `URI`? Any field to hide?
2. Should `pssh` fall through to plain `ssh` (no passbolt) when a host is
   explicitly unlinked, or always offer to link?

## Build order
1. ✅ `pssh doctor` + dependency/connectivity helpers (foundation). — done
2. ✅ SSH config parser with Include expansion + host list. — done
3. ✅ gum host picker → `exec ssh`. — done
4. ✅ Passbolt resolve + clipboard copy. — done
5. ✅ Link/unlink flow + the two store backends. — done
6. ✅ Config file, auto-clear, polish. — done

## Implementation notes
- Single script `pssh` (POSIX `/bin/sh`, parse-clean under dash). Source it with
  `PSSH_LIB=1 . ./pssh` to load functions without running `main` (used by tests).
- Passbolt error classification is **semantic, not substring-naive**: the error
  messages embed the request URL `.../auth/verify.json`, so bare `auth`/`verify`
  matches are unreliable. Probe states: `ok | unconfigured | unreachable | auth |
  error`, checked config → network → auth in that order.
- HostName inference for `passbolt-uri` uses `ssh -G <host>` (ssh resolves its own
  Includes), falling back from the alias to the resolved `hostname`.
- Comment writes are block-scoped via awk: replace-in-place (no dupes), and only
  touch the matching `Host` block.
- **passbolt JSON keys are lowercase** (`id`, `name`, `username`, `uri`,
  `password`) even though the `-c ID -c Name …` column flags and the CEL
  `--filter 'URI == …'` use capitalized names. jq must read the lowercase keys.
- **clip.exe needs UTF-16LE.** On WSL, `clip.exe` reads stdin in the Windows
  codepage and corrupts UTF-8 multibyte bytes — a password with non-ASCII chars
  arrives mangled (→ "permission denied", while a manual copy works). `clip_copy`
  transcodes via `iconv -f UTF-8 -t UTF-16LE` for clip.exe only; native tools
  (wl-copy/xclip/xsel/pbcopy) get UTF-8 directly. Verified byte-exact by sha256.
  Pure-ASCII passwords hide the bug, so test with a non-ASCII secret.
- `--verbose` / `-V` (parsed as a leading flag so ssh args are never mangled)
  traces store resolution, the matched resource (name/username/uri) and password
  length — never the secret. It also warns when several resources share a URI.
- **URI inference is only used when unambiguous.** `resolve_host` sets
  `PSSH_RSTATE` = `comment | uri-unique | ambiguous | none`. A URI match is taken
  only when exactly one resource matches; zero or many (e.g. 14 DB creds sharing a
  host IP) defer to the interactive resource picker rather than auto-picking a
  wrong one. The picker's choice is then persisted via the configured store, so it
  is pinned next time. Resolution is via globals (not `$()`), so the state and id
  survive — a command-substitution subshell would drop them.
