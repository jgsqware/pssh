#!/bin/sh
# pssh — Passbolt-aware SSH launcher
# Pick an SSH host, resolve its linked Passbolt password, copy it to the
# clipboard, then hand off to a normal `ssh` session. The first time you
# connect to an unlinked host, pssh helps you pick a Passbolt resource and
# remembers the association for next time.
#
#   pssh                pick a host, then connect
#   pssh <hostname>     connect straight to a host
#   pssh link <host>    (re)assign the Passbolt resource for a host
#   pssh unlink <host>  remove a host's association
#   pssh doctor         check dependencies + passbolt connectivity

set -eu

PSSH_VERSION="0.1.0"

# --------------------------------------------------------------------------
# Config
# --------------------------------------------------------------------------
# Defaults, overridable by ~/.config/pssh/config (a sourced shell file) or env.
PSSH_CONFIG="${PSSH_CONFIG:-${XDG_CONFIG_HOME:-$HOME/.config}/pssh/config}"

PSSH_STORE="${PSSH_STORE:-ssh-comment}"        # ssh-comment | passbolt-uri
PSSH_DELIVER="${PSSH_DELIVER:-auto}"           # auto | askpass | clipboard
PSSH_CLEAR="${PSSH_CLEAR:-}"                    # clipboard auto-clear secs; empty/0 = off
PSSH_CLIPBOARD="${PSSH_CLIPBOARD:-}"           # explicit clipboard command override
PSSH_PASSBOLT_FLAGS="${PSSH_PASSBOLT_FLAGS:-}" # extra flags for every passbolt call
PSSH_PROBE_TIMEOUT="${PSSH_PROBE_TIMEOUT:-10s}"

SSH_CONFIG="${PSSH_SSH_CONFIG:-$HOME/.ssh/config}"
PSSH_VERBOSE="${PSSH_VERBOSE:-0}"              # 1 = trace what pssh is doing
PSSH_PLUGIN_DIR="${PSSH_PLUGIN_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/pssh/plugins}"

# shellcheck source=/dev/null
[ -f "$PSSH_CONFIG" ] && . "$PSSH_CONFIG"

# --------------------------------------------------------------------------
# Output helpers (degrade gracefully without gum / colors)
# --------------------------------------------------------------------------
have() { command -v "$1" >/dev/null 2>&1; }

if [ -t 2 ]; then
  C_RESET='\033[0m'; C_RED='\033[31m'; C_GREEN='\033[32m'
  C_YELLOW='\033[33m'; C_BLUE='\033[34m'; C_DIM='\033[2m'
else
  C_RESET=''; C_RED=''; C_GREEN=''; C_YELLOW=''; C_BLUE=''; C_DIM=''
fi

vlog()  { [ "$PSSH_VERBOSE" = "1" ] && printf '%b[pssh]%b %s\n' "$C_DIM" "$C_RESET" "$*" >&2 || true; }
info()  { printf '%b%s%b\n' "$C_BLUE"   "$*" "$C_RESET" >&2; }
warn()  { printf '%b%s%b\n' "$C_YELLOW" "$*" "$C_RESET" >&2; }
err()   { printf '%b%s%b\n' "$C_RED"    "$*" "$C_RESET" >&2; }
die()   { err "$*"; exit 1; }

ok_mark()   { printf '%b✓%b' "$C_GREEN"  "$C_RESET"; }
bad_mark()  { printf '%b✗%b' "$C_RED"    "$C_RESET"; }
warn_mark() { printf '%b!%b' "$C_YELLOW" "$C_RESET"; }

status_line() {
  mark="$1"; label="$2"; detail="${3:-}"
  if [ -n "$detail" ]; then
    printf '  %b %s %b— %s%b\n' "$mark" "$label" "$C_DIM" "$detail" "$C_RESET"
  else
    printf '  %b %s\n' "$mark" "$label"
  fi
}

# --------------------------------------------------------------------------
# SSH config parsing (Include expansion, host listing, comment store)
# --------------------------------------------------------------------------
# Echo every ssh config file: the main file plus expanded `Include` globs,
# depth-first. Relative includes are resolved against the including file's dir
# (which is ~/.ssh for the top-level config — matching common setups).
config_files() { _expand_config "$SSH_CONFIG"; }
_expand_config() {
  _f="$1"
  [ -f "$_f" ] || return 0
  printf '%s\n' "$_f"
  _dir=$(dirname "$_f")
  # Pull out Include directives (case-insensitive keyword).
  sed -n 's/^[[:space:]]*[Ii]nclude[[:space:]]\{1,\}//p' "$_f" | while IFS= read -r _pats; do
    for _pat in $_pats; do
      case "$_pat" in
        /*)  _glob="$_pat" ;;
        ~/*) _glob="$HOME/${_pat#~/}" ;;
        *)   _glob="$_dir/$_pat" ;;
      esac
      for _inc in $_glob; do
        [ -f "$_inc" ] && _expand_config "$_inc"
      done
    done
  done
}

# List every concrete (non-wildcard) Host alias across all config files.
list_hosts() {
  config_files | while IFS= read -r _f; do
    awk '
      { line=$0; sub(/#.*/,"",line); n=split(line,a," ")
        if (n>=1 && tolower(a[1])=="host")
          for (i=2;i<=n;i++) if (a[i] !~ /[*?!]/) print a[i] }
    ' "$_f"
  done | awk 'NF && !seen[$0]++'
}

# Echo the `# pssh:` resource id recorded in <host>'s block, if any.
host_comment_id() {
  _target="$1"
  config_files | while IFS= read -r _f; do
    awk -v t="$_target" '
      { raw=$0; c=raw; sub(/#.*/,"",c); n=split(c,a," "); kw=tolower(a[1])
        if (kw=="host"||kw=="match") {
          inblock=0
          if (kw=="host") for (i=2;i<=n;i++) if (a[i]==t) inblock=1
          next
        }
        if (inblock && match(raw,/#[[:space:]]*pssh:[[:space:]]*[^[:space:]]+/)) {
          s=substr(raw,RSTART,RLENGTH); sub(/.*pssh:[[:space:]]*/,"",s); print s; exit
        }
      }' "$_f"
  done | head -n1
}

# Echo the config file that contains <host>'s Host block (first match).
locate_block_file() {
  _target="$1"
  config_files | while IFS= read -r _f; do
    _hit=$(awk -v t="$_target" '
      { c=$0; sub(/#.*/,"",c); n=split(c,a," ")
        if (n>=1 && tolower(a[1])=="host")
          for (i=2;i<=n;i++) if (a[i]==t) { print "yes"; exit } }
    ' "$_f")
    if [ "$_hit" = "yes" ]; then printf '%s\n' "$_f"; break; fi
  done | head -n1
}

# Write/replace `# pssh: <id>` in <host>'s block. Returns 1 if host not found.
write_comment() {
  _host="$1"; _id="$2"
  _f=$(locate_block_file "$_host")
  if [ -z "$_f" ]; then
    warn "'$_host' is not a Host in your ssh config; cannot store a comment."
    warn "Add it to ~/.ssh/config, or set PSSH_STORE=passbolt-uri."
    return 1
  fi
  _tmp="${_f}.pssh.$$"
  awk -v t="$_host" -v id="$_id" '
    { raw=$0; c=raw; sub(/#.*/,"",c); n=split(c,a," "); kw=tolower(a[1])
      if (kw=="host"||kw=="match") {
        inblock=0; print raw
        if (kw=="host") {
          for (i=2;i<=n;i++) if (a[i]==t) inblock=1
          if (inblock) print "    # pssh: " id
        }
        next
      }
      if (inblock && raw ~ /#[[:space:]]*pssh:[[:space:]]*/) next  # drop old link
      print raw
    }
  ' "$_f" > "$_tmp" && cat "$_tmp" > "$_f" && rm -f "$_tmp"
}

# Remove `# pssh:` from <host>'s block. Returns 1 if nothing to remove.
remove_comment() {
  _host="$1"
  [ -n "$(host_comment_id "$_host")" ] || return 1
  _f=$(locate_block_file "$_host"); [ -n "$_f" ] || return 1
  _tmp="${_f}.pssh.$$"
  awk -v t="$_host" '
    { raw=$0; c=raw; sub(/#.*/,"",c); n=split(c,a," "); kw=tolower(a[1])
      if (kw=="host"||kw=="match") {
        inblock=0
        if (kw=="host") for (i=2;i<=n;i++) if (a[i]==t) inblock=1
        print raw; next
      }
      if (inblock && raw ~ /#[[:space:]]*pssh:[[:space:]]*/) next
      print raw
    }
  ' "$_f" > "$_tmp" && cat "$_tmp" > "$_f" && rm -f "$_tmp"
}

# --------------------------------------------------------------------------
# Passbolt helpers
# --------------------------------------------------------------------------
passbolt_cmd() {
  # shellcheck disable=SC2086
  passbolt $PSSH_PASSBOLT_FLAGS "$@"
}

# Run a passbolt call, wrapped in a gum spinner when available.
pb_spin() {
  _title="$1"; shift
  if have gum; then
    # shellcheck disable=SC2086
    gum spin --spinner dot --show-output --title "$_title" -- \
      passbolt $PSSH_PASSBOLT_FLAGS "$@"
  else
    passbolt_cmd "$@"
  fi
}

# Probe connectivity. Sets PSSH_PROBE_STATE (ok|unconfigured|unreachable|auth|
# error) and PSSH_PROBE_ERR (raw message). Set as globals — not echoed — so the
# message survives (a command-substitution subshell would lose it).
#
# Classification order matters: passbolt error messages embed the request URL
# (".../auth/verify.json"), so bare "auth"/"verify" substrings are unreliable.
# Check config, then network, then genuine auth failures.
PSSH_PROBE_STATE=""
PSSH_PROBE_ERR=""
passbolt_probe() {
  PSSH_PROBE_ERR=""
  if passbolt_cmd list resource -j --timeout "$PSSH_PROBE_TIMEOUT" -c ID >/dev/null 2>&1; then
    PSSH_PROBE_STATE=ok
    return 0
  fi
  PSSH_PROBE_ERR=$(passbolt_cmd list resource -j --timeout "$PSSH_PROBE_TIMEOUT" -c ID 2>&1 >/dev/null || true)
  case "$PSSH_PROBE_ERR" in
    *serverAddress*|*"not configured"*|*"no config"*|*"config file"*|*"userPrivateKey"*)
      PSSH_PROBE_STATE=unconfigured ;;
    *"no such host"*|*"dial tcp"*|*"connection refused"*|*"deadline exceeded"*|*"i/o timeout"*|*timeout*|*"TLS handshake"*|*x509*|*certificate*)
      PSSH_PROBE_STATE=unreachable ;;
    *401*|*403*|*Unauthorized*|*Forbidden*|*MFA*|*passphrase*|*"authentication failed"*)
      PSSH_PROBE_STATE=auth ;;
    *)
      PSSH_PROBE_STATE=error ;;
  esac
}

passbolt_help() {
  state="$1"
  case "$state" in
    unconfigured)
      warn "Passbolt is not configured yet. Run something like:"
      cat >&2 <<'EOF'

  passbolt configure \
    --serverAddress https://passbolt.example.com \
    --userPrivateKeyFile ~/passbolt-private.asc
  passbolt verify

EOF
      info "Then re-run pssh. (Full flag list: passbolt configure --help)"
      ;;
    unreachable)
      warn "Passbolt server is configured but unreachable (DNS/network)."
      [ -n "$PSSH_PROBE_ERR" ] && printf '%b%s%b\n' "$C_DIM" "$PSSH_PROBE_ERR" "$C_RESET" >&2
      info "Check your network / VPN, then retry. (passbolt verify to re-test)"
      ;;
    auth)
      warn "Passbolt is configured but the session could not authenticate."
      [ -n "$PSSH_PROBE_ERR" ] && printf '%b%s%b\n' "$C_DIM" "$PSSH_PROBE_ERR" "$C_RESET" >&2
      info "Try:  passbolt verify   (or check --mfaMode if you use MFA)"
      ;;
    *)
      warn "Could not reach Passbolt."
      [ -n "$PSSH_PROBE_ERR" ] && printf '%b%s%b\n' "$C_DIM" "$PSSH_PROBE_ERR" "$C_RESET" >&2
      ;;
  esac
}

# Ensure passbolt is reachable + authenticated, or guide the user and exit.
PSSH_PB_OK=0
ensure_passbolt() {
  [ "$PSSH_PB_OK" = "1" ] && return 0
  have gum && info "Checking Passbolt…"
  passbolt_probe
  vlog "passbolt probe: $PSSH_PROBE_STATE"
  case "$PSSH_PROBE_STATE" in
    ok) PSSH_PB_OK=1 ;;
    *)  echo >&2; passbolt_help "$PSSH_PROBE_STATE"; exit 1 ;;
  esac
}

# Echo the raw JSON array of resources whose URI equals <arg>.
pb_uri_query() {
  passbolt_cmd list resource -j --timeout "$PSSH_PROBE_TIMEOUT" \
    --filter "URI == \"$1\"" -c ID -c Name -c URI 2>/dev/null || true
}

# URI inference for <host>: try the alias, then the resolved HostName/IP. Echo
# a single tab-separated line "<count>\t<firstId>\t<matchedValue>" so callers
# get the match count (to detect ambiguity) and id from one captured result.
pb_uri_resolve() {
  _host="$1"; _matched="$_host"
  _j=$(pb_uri_query "$_host")
  _n=$(printf '%s' "$_j" | jq -r 'length' 2>/dev/null || echo 0); _n=${_n:-0}
  if [ "$_n" -eq 0 ]; then
    _real=$(ssh -G "$_host" 2>/dev/null | awk '$1=="hostname"{print $2; exit}')
    if [ -n "$_real" ] && [ "$_real" != "$_host" ]; then
      _j=$(pb_uri_query "$_real"); _matched="$_real"
      _n=$(printf '%s' "$_j" | jq -r 'length' 2>/dev/null || echo 0); _n=${_n:-0}
    fi
  fi
  _id=$(printf '%s' "$_j" | jq -r '.[0].id // empty' 2>/dev/null || true)
  printf '%s\t%s\t%s' "$_n" "$_id" "$_matched"
}

# Confident URI id for unlink: only return one when exactly one resource matches.
uri_resource_id() {
  _r=$(pb_uri_resolve "$1")
  _n=$(printf '%s' "$_r" | cut -f1)
  [ "${_n:-0}" -eq 1 ] || return 0
  printf '%s' "$_r" | cut -f2
}

# Resolve a host, setting globals (no command-substitution subshell, so state
# survives). Reads both stores; URI inference is used ONLY when it is
# unambiguous — zero or multiple matches defer to the interactive picker.
#   PSSH_RID    — resolved resource id (empty unless comment/uri-unique)
#   PSSH_RSTATE — comment | uri-unique | ambiguous | none
PSSH_RID=""; PSSH_RSTATE="none"
resolve_host() {
  _host="$1"
  PSSH_RID=""; PSSH_RSTATE="none"
  vlog "resolving '$_host' (store=$PSSH_STORE; both stores are read)"
  _cid=$(host_comment_id "$_host")
  vlog "ssh-comment lookup: ${_cid:-none}"
  if [ -n "$_cid" ]; then PSSH_RID="$_cid"; PSSH_RSTATE="comment"; return 0; fi

  _r=$(pb_uri_resolve "$_host")
  _n=$(printf '%s' "$_r" | cut -f1)
  _id=$(printf '%s' "$_r" | cut -f2)
  _matched=$(printf '%s' "$_r" | cut -f3)
  vlog "uri inference: ${_n:-0} candidate(s) for '$_matched'"
  if [ "${_n:-0}" -eq 1 ]; then
    PSSH_RID="$_id"; PSSH_RSTATE="uri-unique"
    vlog "single URI match → ${PSSH_RID:-none}"
  elif [ "${_n:-0}" -gt 1 ]; then
    PSSH_RSTATE="ambiguous"
    vlog "ambiguous: $_n resources share URI '$_matched' — deferring to picker"
  fi
  return 0
}

# Fetch the password for a resource id.
fetch_password() {
  _out=$(pb_spin "Fetching secret…" get resource --id "$1" -j) || return 1
  printf '%s' "$_out" | jq -r '.password // .Password // empty'
}

# --------------------------------------------------------------------------
# Clipboard
# --------------------------------------------------------------------------
detect_clipboard() {
  if [ -n "$PSSH_CLIPBOARD" ]; then echo "$PSSH_CLIPBOARD"; return; fi
  if have wl-copy;  then echo "wl-copy"; return; fi
  if have xclip;    then echo "xclip -selection clipboard"; return; fi
  if have xsel;     then echo "xsel -b"; return; fi
  if have pbcopy;   then echo "pbcopy"; return; fi
  if have clip.exe; then echo "clip.exe"; return; fi
  echo ""
}

# Copy stdin to the clipboard via <tool>. clip.exe (WSL → Windows) reads its
# input in the Windows codepage and mangles UTF-8 multibyte bytes, so passwords
# with non-ASCII characters arrive corrupted. Transcode to UTF-16LE first, which
# clip.exe accepts losslessly. Native tools take UTF-8 directly.
clip_copy() {
  _tool="$1"
  case "$_tool" in
    clip.exe|*/clip.exe)
      if have iconv; then
        iconv -f UTF-8 -t UTF-16LE | clip.exe
      else
        warn "iconv not found; clip.exe may corrupt non-ASCII passwords."
        clip.exe
      fi ;;
    *)
      # shellcheck disable=SC2086
      $_tool ;;
  esac
}

# --------------------------------------------------------------------------
# Password delivery to ssh (askpass — no clipboard, no argv, no echo)
# --------------------------------------------------------------------------
# Absolute path to this script, so ssh can exec it as the SSH_ASKPASS helper.
self_path() {
  _p="$0"
  case "$_p" in
    */*) ;;
    *) _p=$(command -v -- "$_p" 2>/dev/null || echo "$_p") ;;
  esac
  case "$_p" in
    /*) printf '%s' "$_p" ;;
    *)  printf '%s/%s' "$(cd "$(dirname "$_p")" && pwd)" "$(basename "$_p")" ;;
  esac
}

# True if ssh is new enough for SSH_ASKPASS_REQUIRE=force (OpenSSH >= 8.4),
# which lets the askpass helper supply the password even with a controlling tty.
ssh_supports_askpass() {
  _v=$(ssh -V 2>&1 | sed -n 's/.*OpenSSH_\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1 \2/p')
  # shellcheck disable=SC2086
  set -- $_v
  _maj=${1:-0}; _min=${2:-0}
  [ "$_maj" -gt 8 ] || { [ "$_maj" -eq 8 ] && [ "$_min" -ge 4 ]; }
}

# Resolve the configured delivery method to a concrete one (askpass|clipboard).
resolve_deliver() {
  case "$PSSH_DELIVER" in
    askpass|clipboard) printf '%s' "$PSSH_DELIVER" ;;
    auto|"")
      if ssh_supports_askpass; then printf 'askpass'; else printf 'clipboard'; fi ;;
    *) die "Unknown PSSH_DELIVER='$PSSH_DELIVER' (use auto|askpass|clipboard)." ;;
  esac
}

# --------------------------------------------------------------------------
# Pickers
# --------------------------------------------------------------------------
pick_host() {
  _hosts=$(list_hosts)
  [ -n "$_hosts" ] || die "No hosts found in $SSH_CONFIG"
  if have gum; then
    printf '%s\n' "$_hosts" | gum filter --placeholder "Pick a host…" --height 15
  else
    printf '%s\n' "$_hosts" | cat -n >&2
    printf 'Host number: ' >&2; read -r _n
    printf '%s\n' "$_hosts" | sed -n "${_n}p"
  fi
}

# Pick a Passbolt resource interactively; echo its id. Network call inside.
pick_resource() {
  ensure_passbolt
  _json=$(pb_spin "Loading resources…" list resource -j -c ID -c Name -c Username -c URI) \
    || die "Could not list Passbolt resources."
  # display<TAB>id, sorted by name
  # passbolt JSON output uses lowercase keys (id/name/username/uri).
  _rows=$(printf '%s' "$_json" | jq -r '
    .[] | [ "\(.name)  ·  \(.username // "")  ·  \(.uri // "")", .id ] | @tsv' | sort -f)
  [ -n "$_rows" ] || die "No Passbolt resources available."
  if have gum; then
    _choice=$(printf '%s\n' "$_rows" | cut -f1 \
      | gum filter --placeholder "Pick a resource…" --height 15) || return 1
  else
    printf '%s\n' "$_rows" | cut -f1 | cat -n >&2
    printf 'Resource number: ' >&2; read -r _n
    _choice=$(printf '%s\n' "$_rows" | cut -f1 | sed -n "${_n}p")
  fi
  [ -n "$_choice" ] || return 1
  printf '%s\n' "$_rows" | awk -F'\t' -v c="$_choice" '$1==c{print $2; exit}'
}

# --------------------------------------------------------------------------
# Linking
# --------------------------------------------------------------------------
# Interactive link: pick a resource, persist via the configured store, echo id.
link_resource() {
  _host="$1"
  _id=$(pick_resource) || return 1
  [ -n "$_id" ] || return 1
  case "$PSSH_STORE" in
    ssh-comment)
      write_comment "$_host" "$_id" || true ;;
    passbolt-uri)
      ensure_passbolt
      pb_spin "Linking…" update resource --id "$_id" --uri "$_host" >/dev/null \
        && info "Set resource URI to '$_host'." \
        || warn "Could not update resource URI." ;;
    *)
      die "Unknown PSSH_STORE='$PSSH_STORE'." ;;
  esac
  printf '%s' "$_id"
}

# --------------------------------------------------------------------------
# Connect
# --------------------------------------------------------------------------
notify_copied() {
  _host="$1"; _clip="$2"
  _msg="Password for $_host copied to clipboard ($_clip)."
  if [ -n "$PSSH_CLEAR" ] && [ "$PSSH_CLEAR" != "0" ]; then
    _msg="$_msg  Auto-clears in ${PSSH_CLEAR}s."
  fi
  if have gum; then
    gum style --foreground 42 --border rounded --padding "0 1" "$_msg" >&2
  else
    info "$_msg"
  fi
}

# Resolve <host>, linking interactively when there is no confident match, then
# fetch and echo its password on stdout (diagnostics/pickers go to stderr).
# Shared by cmd_connect and the plugin dispatcher.
obtain_password() {
  _host="$1"
  resolve_host "$_host"
  _id="$PSSH_RID"
  if [ -z "$_id" ]; then
    case "$PSSH_RSTATE" in
      ambiguous)
        warn "Can't safely infer the Passbolt resource for '$_host' —"
        warn "several resources share its address. Pick the right one:" ;;
      *)
        info "No Passbolt link for '$_host' yet — pick one." ;;
    esac
    _id=$(link_resource "$_host") || return 1
    [ -n "$_id" ] || return 1
  fi
  ensure_passbolt
  vlog "resolved resource id: $_id"
  _json=$(pb_spin "Fetching secret…" get resource --id "$_id" -j) || return 1
  if [ "$PSSH_VERBOSE" = "1" ]; then
    vlog "resource: name='$(printf '%s' "$_json" | jq -r '.name // ""')' \
username='$(printf '%s' "$_json" | jq -r '.username // ""')' \
uri='$(printf '%s' "$_json" | jq -r '.uri // ""')'"
  fi
  _pw=$(printf '%s' "$_json" | jq -r '.password // .Password // empty')
  [ -n "$_pw" ] || { err "Resource $_id has no password."; return 1; }
  if [ "$PSSH_VERBOSE" = "1" ]; then
    _plen=$(printf '%s' "$_pw" | wc -c | tr -d ' ')
    _note=""
    printf '%s' "$_pw" | LC_ALL=C grep -q '[[:space:]]' && _note=" — contains whitespace" || true
    case "$_pw" in *[!\ -~]*) _note="$_note — contains non-ASCII/control chars" ;; esac
    vlog "password: ${_plen} byte(s)${_note}"
  fi
  printf '%s' "$_pw"
}

# Export the env that turns this script into ssh's askpass helper for <password>.
setup_askpass_env() {
  export PSSH_ASKPASS=1 PSSH_ASKPASS_VALUE="$1"
  export SSH_ASKPASS="$(self_path)" SSH_ASKPASS_REQUIRE=force
}

cmd_connect() {
  _host="${1:-}"
  [ "$#" -gt 0 ] && shift || true
  if [ -z "$_host" ]; then
    _host=$(pick_host) || die "No host selected."
    [ -n "$_host" ] || die "No host selected."
  fi

  _pw=$(obtain_password "$_host") || die "Could not obtain the password for '$_host'."

  _method=$(resolve_deliver)
  vlog "delivery method: $_method"
  case "$_method" in
    askpass)
      if have gum; then
        gum style --foreground 42 --border rounded --padding "0 1" \
          "Connecting to $_host — password supplied to ssh directly (not copied, not shown)." >&2
      else
        info "Connecting to $_host — password supplied to ssh directly."
      fi
      # ssh execs $SSH_ASKPASS with the password in the env it inherits; the
      # secret is never an argument, never echoed, never on the clipboard. It is
      # not forwarded to the remote (ssh only sends SendEnv vars).
      setup_askpass_env "$_pw"
      vlog "exec: ssh $_host $*  (askpass)"
      exec ssh "$_host" "$@"
      ;;
    clipboard)
      _clip=$(detect_clipboard)
      [ -n "$_clip" ] || die "No clipboard tool available (see: pssh doctor)."
      vlog "clipboard tool: $_clip"
      printf '%s' "$_pw" | clip_copy "$_clip"
      notify_copied "$_host" "$_clip"
      if [ -n "$PSSH_CLEAR" ] && [ "$PSSH_CLEAR" != "0" ]; then
        vlog "scheduling clipboard clear in ${PSSH_CLEAR}s"
        ( sleep "$PSSH_CLEAR"; printf '' | clip_copy "$_clip" ) >/dev/null 2>&1 &
      fi
      _pw=""
      vlog "exec: ssh $_host $*"
      exec ssh "$_host" "$@"
      ;;
  esac
}

# --------------------------------------------------------------------------
# Plugins
# --------------------------------------------------------------------------
# A plugin is an executable named `pssh-<name>` in $PSSH_PLUGIN_DIR or on PATH.
# `pssh <name> <host> [args…]` resolves the host's Passbolt password, wires up
# the askpass env, and exec's the plugin — so the plugin's own ssh calls (e.g.
# ssh-lazysql's master connection) authenticate with no extra code.
plugin_path() {
  _name="$1"
  case "$_name" in */*|.*|"") return 1 ;; esac   # guard: plugin names are bare words
  if [ -n "$PSSH_PLUGIN_DIR" ] && [ -x "$PSSH_PLUGIN_DIR/pssh-$_name" ]; then
    printf '%s' "$PSSH_PLUGIN_DIR/pssh-$_name"; return 0
  fi
  command -v "pssh-$_name" 2>/dev/null && return 0
  return 1
}

cmd_plugins() {
  info "Plugin dir: $PSSH_PLUGIN_DIR"
  _found=0
  for _d in "$PSSH_PLUGIN_DIR" $(printf '%s' "${PATH:-}" | tr ':' ' '); do
    [ -d "$_d" ] || continue
    for _f in "$_d"/pssh-*; do
      [ -x "$_f" ] || continue
      _n=$(basename "$_f"); _n=${_n#pssh-}
      status_line "$(ok_mark)" "$_n" "$_f"; _found=1
    done
  done
  [ "$_found" = "1" ] || warn "No plugins found (install an executable named pssh-<name>)."
}

cmd_plugin() {
  _pp="$1"; shift
  _name=$(basename "$_pp"); _name=${_name#pssh-}
  # The host is the first non-option argument; all args pass through unchanged so
  # the plugin keeps its own flag handling (e.g. ssh-lazysql's -t). With no host
  # given (e.g. `pssh pg`), show the host picker and prepend the choice.
  _host=""
  for _a in "$@"; do case "$_a" in -*) ;; *) _host="$_a"; break ;; esac; done
  if [ -z "$_host" ]; then
    _host=$(pick_host) || die "No host selected."
    [ -n "$_host" ] || die "No host selected."
    set -- "$_host" "$@"
  fi

  _pw=$(obtain_password "$_host") || die "Could not obtain the password for '$_host'."

  _method=$(resolve_deliver)
  if [ "$_method" = "askpass" ]; then
    have gum && gum style --foreground 42 --border rounded --padding "0 1" \
      "Running '$_name' on $_host — ssh password supplied automatically." >&2 \
      || info "Running '$_name' on $_host — ssh password supplied automatically."
    setup_askpass_env "$_pw"
  else
    # ssh too old for askpass: best effort — stage on the clipboard so the user
    # can paste at the plugin's own password prompt.
    _clip=$(detect_clipboard)
    if [ -n "$_clip" ]; then
      printf '%s' "$_pw" | clip_copy "$_clip"
      warn "ssh lacks askpass support; password copied to clipboard — paste when '$_name' prompts."
    else
      warn "No askpass and no clipboard available; '$_name' will prompt for the password."
    fi
  fi
  _pw=""
  vlog "exec plugin: $_pp $*"
  exec "$_pp" "$@"
}

# --------------------------------------------------------------------------
# Subcommands
# --------------------------------------------------------------------------
cmd_link() {
  _host="${1:-}"
  [ -n "$_host" ] || _host=$(pick_host) || die "No host selected."
  [ -n "$_host" ] || die "No host selected."
  _id=$(link_resource "$_host") || die "Linking cancelled."
  [ -n "$_id" ] && info "Linked '$_host' → $_id"
}

cmd_unlink() {
  _host="${1:-}"
  [ -n "$_host" ] || die "Usage: pssh unlink <host>"
  case "$PSSH_STORE" in
    ssh-comment)
      if remove_comment "$_host"; then info "Removed pssh link for '$_host'."
      else warn "No ssh-comment link found for '$_host'."; fi ;;
    passbolt-uri)
      _id=$(uri_resource_id "$_host")
      [ -n "$_id" ] || die "No URI-linked resource for '$_host'."
      ensure_passbolt
      pb_spin "Unlinking…" update resource --id "$_id" --uri "" >/dev/null \
        && info "Cleared URI on resource $_id." \
        || warn "Could not clear resource URI." ;;
    *)
      die "Unknown PSSH_STORE='$PSSH_STORE'." ;;
  esac
}

cmd_doctor() {
  rc=0
  printf '%bpssh doctor%b  (v%s)\n\n' "$C_BLUE" "$C_RESET" "$PSSH_VERSION"

  if have gum; then status_line "$(ok_mark)" "gum" "$(gum --version 2>/dev/null | head -1)"
  else status_line "$(warn_mark)" "gum" "not found — pickers fall back to numbered prompts"; fi

  if have passbolt; then status_line "$(ok_mark)" "passbolt" "$(passbolt --version 2>/dev/null | head -1)"
  else status_line "$(bad_mark)" "passbolt" "not found — install the passbolt CLI"; rc=1; fi

  if have jq; then status_line "$(ok_mark)" "jq" "$(jq --version 2>/dev/null)"
  else status_line "$(bad_mark)" "jq" "not found — required to parse passbolt JSON"; rc=1; fi

  clip=$(detect_clipboard)
  if [ -n "$clip" ]; then
    case "$clip" in
      clip.exe|*/clip.exe)
        if have iconv; then status_line "$(ok_mark)" "clipboard" "$clip (UTF-16LE via iconv)"
        else status_line "$(warn_mark)" "clipboard" "$clip — iconv missing, non-ASCII passwords may corrupt"; fi ;;
      *) status_line "$(ok_mark)" "clipboard" "$clip" ;;
    esac
  else status_line "$(bad_mark)" "clipboard" "none (wl-copy/xclip/xsel/pbcopy/clip.exe)"; rc=1; fi

  if [ -r "$SSH_CONFIG" ]; then
    nhosts=$(list_hosts | wc -l | tr -d ' ')
    status_line "$(ok_mark)" "ssh config" "$SSH_CONFIG ($nhosts hosts)"
  else
    status_line "$(bad_mark)" "ssh config" "not readable: $SSH_CONFIG"; rc=1
  fi

  case "$PSSH_STORE" in
    ssh-comment|passbolt-uri) status_line "$(ok_mark)" "store" "$PSSH_STORE" ;;
    *) status_line "$(bad_mark)" "store" "unknown PSSH_STORE='$PSSH_STORE'"; rc=1 ;;
  esac

  _dm=$(resolve_deliver 2>/dev/null || echo "?")
  if [ "$_dm" = "askpass" ]; then
    status_line "$(ok_mark)" "delivery" "askpass — fed to ssh directly (PSSH_DELIVER=$PSSH_DELIVER)"
  elif [ "$_dm" = "clipboard" ]; then
    _why=""; ssh_supports_askpass || _why=" (ssh too old for askpass)"
    status_line "$(ok_mark)" "delivery" "clipboard (PSSH_DELIVER=$PSSH_DELIVER)$_why"
  else
    status_line "$(bad_mark)" "delivery" "invalid PSSH_DELIVER='$PSSH_DELIVER'"; rc=1
  fi

  _np=0
  for _d in "$PSSH_PLUGIN_DIR" $(printf '%s' "${PATH:-}" | tr ':' ' '); do
    [ -d "$_d" ] || continue
    for _f in "$_d"/pssh-*; do [ -x "$_f" ] && _np=$((_np+1)); done
  done
  status_line "$(ok_mark)" "plugins" "$_np found ($PSSH_PLUGIN_DIR + PATH)"

  if have passbolt; then
    have gum && info "Probing Passbolt…"
    passbolt_probe
    case "$PSSH_PROBE_STATE" in
      ok)           status_line "$(ok_mark)"  "passbolt auth" "reachable + authenticated" ;;
      unconfigured) status_line "$(warn_mark)" "passbolt auth" "not configured"; echo; passbolt_help unconfigured; rc=1 ;;
      unreachable)  status_line "$(bad_mark)"  "passbolt auth" "server unreachable (DNS/network)"; echo; passbolt_help unreachable; rc=1 ;;
      auth)         status_line "$(bad_mark)"  "passbolt auth" "configured but not authenticated"; echo; passbolt_help auth; rc=1 ;;
      *)            status_line "$(bad_mark)"  "passbolt auth" "error"; echo; passbolt_help error; rc=1 ;;
    esac
  fi

  echo
  if [ "$rc" -eq 0 ]; then info "All good. pssh is ready."
  else warn "Some checks need attention (see above)."; fi
  return "$rc"
}

usage() {
  cat >&2 <<EOF
pssh — Passbolt-aware SSH launcher (v$PSSH_VERSION)

Usage:
  pssh                pick a host, then connect
  pssh <hostname>     connect straight to a host
  pssh link <host>    (re)assign the Passbolt resource for a host
  pssh unlink <host>  remove a host's association
  pssh <name> <host>  run plugin pssh-<name> with the host's password (askpass)
  pssh plugins        list available plugins
  pssh doctor         check dependencies + passbolt connectivity
  pssh help           show this help

Options:
  --verbose, -V       trace resolution, matched resource + delivery (no secret)
                      e.g. pssh --verbose <host>   (place before the host)

Delivery (PSSH_DELIVER): auto | askpass | clipboard
  askpass   feed the password straight to ssh — never copied, shown, or stored
  clipboard copy to clipboard, you paste at the prompt (PSSH_CLEAR=secs to clear)

Config: $PSSH_CONFIG
Store:  $PSSH_STORE   Delivery: $PSSH_DELIVER   Clipboard auto-clear: ${PSSH_CLEAR:-off}
EOF
}

main() {
  # SSH_ASKPASS mode: ssh exec's this script with the prompt as $1. Answer
  # password/passphrase prompts with the secret; decline anything else (e.g. a
  # host-key "yes/no" prompt) so we never auto-trust an unknown host.
  if [ "${PSSH_ASKPASS:-0}" = "1" ]; then
    case "${1:-}" in
      *[Pp]assword*|*[Pp]assphrase*|"") printf '%s\n' "${PSSH_ASKPASS_VALUE:-}" ;;
      *) exit 1 ;;
    esac
    exit 0
  fi

  # Leading --verbose / -V toggles tracing; everything after is left intact
  # (so ssh args, which may contain spaces, are never mangled).
  while [ "${1:-}" = "--verbose" ] || [ "${1:-}" = "-V" ]; do
    PSSH_VERBOSE=1; shift
  done
  cmd="${1:-}"
  case "$cmd" in
    doctor)         shift; cmd_doctor "$@" ;;
    link)           shift; cmd_link "$@" ;;
    unlink)         shift; cmd_unlink "$@" ;;
    plugins)        shift; cmd_plugins ;;
    help|-h|--help) usage ;;
    "")             cmd_connect "" ;;
    *)
      # A plugin (pssh-<cmd>) takes precedence over a same-named host.
      _pp=$(plugin_path "$cmd" 2>/dev/null || true)
      shift
      if [ -n "$_pp" ]; then cmd_plugin "$_pp" "$@"
      else cmd_connect "$cmd" "$@"; fi
      ;;
  esac
}

# Allow sourcing for tests: `PSSH_LIB=1 . ./pssh` loads functions without running.
[ "${PSSH_LIB:-0}" = "1" ] || main "$@"
