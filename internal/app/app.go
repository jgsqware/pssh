// Package app wires the pieces together: dispatch, host/resource resolution,
// password delivery, plugins, and doctor.
package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jgsqware/pssh/internal/clip"
	"github.com/jgsqware/pssh/internal/config"
	"github.com/jgsqware/pssh/internal/passbolt"
	"github.com/jgsqware/pssh/internal/sshcfg"
	"github.com/jgsqware/pssh/internal/ui"
)

const Version = "1.0.0"

// App holds the resolved config and collaborators.
type App struct {
	cfg  config.Config
	ssh  *sshcfg.Config
	pb   *passbolt.Client
	pbOK bool
}

func New(cfg config.Config) *App {
	ui.SetVerbose(cfg.Verbose)
	return &App{
		cfg: cfg,
		ssh: sshcfg.New(cfg.SSHConfig),
		pb:  passbolt.New(cfg.PassboltArgs, cfg.ProbeTimeout),
	}
}

func (a *App) die(format string, v ...any) {
	ui.Err(format, v...)
	os.Exit(1)
}

// Run dispatches a command line (already stripped of the program name).
func (a *App) Run(args []string) int {
	// Leading --verbose / -V toggles tracing; the rest is passed through intact.
	for len(args) > 0 && (args[0] == "--verbose" || args[0] == "-V") {
		a.cfg.Verbose = true
		ui.SetVerbose(true)
		args = args[1:]
	}

	if len(args) == 0 {
		a.connect("", nil)
		return 0
	}
	switch args[0] {
	case "doctor":
		return a.doctor()
	case "link":
		a.cmdLink(args[1:])
	case "unlink":
		a.cmdUnlink(args[1:])
	case "plugins":
		a.listPlugins()
	case "help", "-h", "--help":
		a.usage()
	default:
		if p := a.pluginPath(args[0]); p != "" {
			a.runPlugin(p, args[1:])
		} else {
			a.connect(args[0], args[1:])
		}
	}
	return 0
}

// ---- resolution -----------------------------------------------------------

// resolve maps a host to a resource id. state is comment|uri-unique|ambiguous|none.
func (a *App) resolve(host string) (id, state string) {
	ui.Vlog("resolving %q (store=%s; both stores are read)", host, a.cfg.Store)
	if cid := a.ssh.CommentID(host); cid != "" {
		ui.Vlog("ssh-comment lookup: %s", cid)
		return cid, "comment"
	}
	ui.Vlog("ssh-comment lookup: none")

	a.ensurePassbolt() // URI inference needs the server
	matched := host
	ms, err := a.pb.FindByURI(host)
	if err != nil {
		ui.Vlog("uri lookup error: %v", err)
		return "", "none"
	}
	if len(ms) == 0 {
		if real := a.ssh.ResolvedHostName(host); real != "" {
			ui.Vlog("inferred HostName %q from ssh config; retrying URI match", real)
			matched = real
			ms, _ = a.pb.FindByURI(real)
		}
	}
	ui.Vlog("uri inference: %d candidate(s) for %q", len(ms), matched)
	switch {
	case len(ms) == 1:
		ui.Vlog("single URI match → %s", ms[0].ID)
		return ms[0].ID, "uri-unique"
	case len(ms) > 1:
		ui.Vlog("ambiguous: %d resources share URI %q — deferring to picker", len(ms), matched)
		return "", "ambiguous"
	default:
		return "", "none"
	}
}

// obtainPassword resolves the host (linking interactively when there is no
// confident match) and returns the decrypted password.
func (a *App) obtainPassword(host string) string {
	id, state := a.resolve(host)
	if id == "" {
		switch state {
		case "ambiguous":
			ui.Warn("Can't safely infer the Passbolt resource for %q —", host)
			ui.Warn("several resources share its address. Pick the right one:")
		default:
			ui.Info("No Passbolt link for %q yet — pick one.", host)
		}
		var err error
		id, err = a.link(host)
		if err != nil {
			a.die("Linking cancelled.")
		}
	}
	a.ensurePassbolt()
	ui.Vlog("resolved resource id: %s", id)
	pw, err := a.pb.Password(id)
	if err != nil {
		state, msg := a.pb.Probe()
		a.passboltHelp(state, msg)
		a.die("Failed to fetch the secret.")
	}
	if pw == "" {
		a.die("Resource %s has no password.", id)
	}
	if a.cfg.Verbose {
		note := ""
		if strings.ContainsAny(pw, " \t\r\n") {
			note += " — contains whitespace"
		}
		if !isASCII(pw) {
			note += " — contains non-ASCII chars"
		}
		ui.Vlog("password: %d byte(s)%s", len(pw), note)
	}
	return pw
}

// ---- linking --------------------------------------------------------------

func (a *App) link(host string) (string, error) {
	a.ensurePassbolt()
	rs, err := a.pb.List()
	if err != nil {
		return "", fmt.Errorf("could not list Passbolt resources: %w", err)
	}
	if len(rs) == 0 {
		return "", fmt.Errorf("no Passbolt resources available")
	}
	type row struct{ disp, id string }
	rows := make([]row, 0, len(rs))
	for _, r := range rs {
		rows = append(rows, row{fmt.Sprintf("%s  ·  %s  ·  %s", r.Name, r.Username, r.URI), r.ID})
	}
	sort.Slice(rows, func(i, j int) bool {
		return strings.ToLower(rows[i].disp) < strings.ToLower(rows[j].disp)
	})
	items := make([]string, len(rows))
	for i, r := range rows {
		items[i] = r.disp
	}
	choice, err := ui.Pick(items, "Pick a resource…")
	if err != nil {
		return "", err
	}
	var id string
	for _, r := range rows {
		if r.disp == choice {
			id = r.id
			break
		}
	}
	if id == "" {
		return "", fmt.Errorf("no resource selected")
	}
	a.persist(host, id)
	return id, nil
}

func (a *App) persist(host, id string) {
	switch a.cfg.Store {
	case "ssh-comment":
		if err := a.ssh.WriteComment(host, id); err != nil {
			ui.Warn("%v", err)
		}
	case "passbolt-uri":
		a.ensurePassbolt()
		if err := a.pb.SetURI(id, host); err != nil {
			ui.Warn("Could not update resource URI: %v", err)
		} else {
			ui.Info("Set resource URI to %q.", host)
		}
	default:
		a.die("Unknown PSSH_STORE=%q.", a.cfg.Store)
	}
}

// ---- connect & plugins ----------------------------------------------------

func (a *App) connect(host string, sshArgs []string) {
	if host == "" {
		host = a.pickHost()
	}
	pw := a.obtainPassword(host)
	argv := append([]string{"ssh", host}, sshArgs...)
	a.deliver(host, pw, "ssh", argv, "")
}

func (a *App) runPlugin(pluginPath string, args []string) {
	name := strings.TrimPrefix(filepath.Base(pluginPath), "pssh-")
	host := firstNonOption(args)
	if host == "" {
		host = a.pickHost()
		args = append([]string{host}, args...)
	}
	pw := a.obtainPassword(host)
	argv := append([]string{filepath.Base(pluginPath)}, args...)
	a.deliver(host, pw, pluginPath, argv, name)
}

func (a *App) pickHost() string {
	hosts := a.ssh.Hosts()
	if len(hosts) == 0 {
		a.die("No hosts found in %s", a.cfg.SSHConfig)
	}
	choice, err := ui.Pick(hosts, "Pick a host…")
	if err != nil {
		a.die("No host selected.")
	}
	return choice
}

// ---- delivery -------------------------------------------------------------

// deliver hands the password to ssh (askpass) or the clipboard, then replaces
// this process with execPath. pluginName is "" for a plain connect.
func (a *App) deliver(host, pw, execPath string, argv []string, pluginName string) {
	method := a.deliverMethod()
	ui.Vlog("delivery method: %s", method)
	full := a.lookExec(execPath)

	switch method {
	case "askpass":
		if pluginName != "" {
			ui.Box("Running '%s' on %s — ssh password supplied automatically.", pluginName, host)
		} else {
			ui.Box("Connecting to %s — password supplied to ssh directly (not copied, not shown).", host)
		}
		a.execReplace(full, argv, a.askpassEnv(pw))
	case "clipboard":
		tool := clip.Detect(a.cfg.Clipboard)
		if tool == nil {
			if pluginName != "" {
				ui.Warn("No askpass and no clipboard; '%s' will prompt for the password.", pluginName)
				a.execReplace(full, argv, os.Environ())
			}
			a.die("No clipboard tool available (see: pssh doctor).")
		}
		if err := clip.Copy(tool, pw); err != nil {
			a.die("Clipboard copy failed: %v", err)
		}
		a.notifyCopied(host, strings.Join(tool, " "))
		if a.cfg.ClearActive() {
			a.spawnClipClear()
		}
		a.execReplace(full, argv, os.Environ())
	}
}

func (a *App) notifyCopied(host, tool string) {
	msg := fmt.Sprintf("Password for %s copied to clipboard (%s).", host, tool)
	if a.cfg.ClearActive() {
		msg += fmt.Sprintf("  Auto-clears in %ss.", a.cfg.Clear)
	}
	ui.Box("%s", msg)
}

// deliverMethod resolves auto -> askpass|clipboard.
func (a *App) deliverMethod() string {
	switch a.cfg.Deliver {
	case "askpass", "clipboard":
		return a.cfg.Deliver
	case "", "auto":
		if sshSupportsAskpass() {
			return "askpass"
		}
		return "clipboard"
	default:
		a.die("Unknown PSSH_DELIVER=%q (use auto|askpass|clipboard).", a.cfg.Deliver)
		return ""
	}
}

func (a *App) askpassEnv(pw string) []string {
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	env := os.Environ()
	env = append(env,
		"PSSH_ASKPASS=1",
		"PSSH_ASKPASS_VALUE="+pw,
		"SSH_ASKPASS="+self,
		"SSH_ASKPASS_REQUIRE=force",
	)
	return env
}

func (a *App) lookExec(name string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

func (a *App) execReplace(path string, argv, env []string) {
	ui.Vlog("exec: %s", strings.Join(argv, " "))
	if err := syscall.Exec(path, argv, env); err != nil {
		a.die("exec %s failed: %v", path, err)
	}
}

// spawnClipClear launches a detached helper that clears the clipboard later,
// surviving this process's replacement by exec.
func (a *App) spawnClipClear() {
	secs := a.cfg.Clear
	self, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(self, "__clipclear", secs)
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	_ = cmd.Start()
	ui.Vlog("scheduling clipboard clear in %ss", secs)
}

// ---- passbolt connectivity ------------------------------------------------

func (a *App) ensurePassbolt() {
	if a.pbOK {
		return
	}
	if ui.HasGum() {
		ui.Info("Checking Passbolt…")
	}
	state, msg := a.pb.Probe()
	ui.Vlog("passbolt probe: %s", state)
	if state != passbolt.OK {
		fmt.Fprintln(os.Stderr)
		a.passboltHelp(state, msg)
		os.Exit(1)
	}
	a.pbOK = true
}

func (a *App) passboltHelp(state passbolt.State, msg string) {
	switch state {
	case passbolt.Unconfigured:
		ui.Warn("Passbolt is not configured yet. Run something like:")
		fmt.Fprint(os.Stderr, `
  passbolt configure \
    --serverAddress https://passbolt.example.com \
    --userPrivateKeyFile ~/passbolt-private.asc
  passbolt verify

`)
		ui.Info("Then re-run pssh. (Full flag list: passbolt configure --help)")
	case passbolt.Unreachable:
		ui.Warn("Passbolt server is configured but unreachable (DNS/network).")
		dim(msg)
		ui.Info("Check your network / VPN, then retry. (passbolt verify to re-test)")
	case passbolt.Auth:
		ui.Warn("Passbolt is configured but the session could not authenticate.")
		dim(msg)
		ui.Info("Try:  passbolt verify   (or check --mfaMode if you use MFA)")
	default:
		ui.Warn("Could not reach Passbolt.")
		dim(msg)
	}
}

func dim(msg string) {
	if s := strings.TrimSpace(msg); s != "" {
		fmt.Fprintln(os.Stderr, "\033[2m"+s+"\033[0m")
	}
}

// ---- subcommands ----------------------------------------------------------

func (a *App) cmdLink(args []string) {
	host := firstNonOption(args)
	if host == "" {
		host = a.pickHost()
	}
	id, err := a.link(host)
	if err != nil {
		a.die("Linking cancelled.")
	}
	ui.Info("Linked %q → %s", host, id)
}

func (a *App) cmdUnlink(args []string) {
	host := firstNonOption(args)
	if host == "" {
		a.die("Usage: pssh unlink <host>")
	}
	switch a.cfg.Store {
	case "ssh-comment":
		removed, err := a.ssh.RemoveComment(host)
		if err != nil {
			a.die("%v", err)
		}
		if removed {
			ui.Info("Removed pssh link for %q.", host)
		} else {
			ui.Warn("No ssh-comment link found for %q.", host)
		}
	case "passbolt-uri":
		a.ensurePassbolt()
		ms, _ := a.pb.FindByURI(host)
		if len(ms) != 1 {
			a.die("No unambiguous URI-linked resource for %q.", host)
		}
		if err := a.pb.SetURI(ms[0].ID, ""); err != nil {
			a.die("Could not clear resource URI: %v", err)
		}
		ui.Info("Cleared URI on resource %s.", ms[0].ID)
	default:
		a.die("Unknown PSSH_STORE=%q.", a.cfg.Store)
	}
}

// ---- plugins --------------------------------------------------------------

var pluginNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_-]*$`)

func (a *App) pluginPath(name string) string {
	if !pluginNameRe.MatchString(name) {
		return ""
	}
	cand := filepath.Join(a.cfg.PluginDir, "pssh-"+name)
	if isExec(cand) {
		return cand
	}
	if p, err := exec.LookPath("pssh-" + name); err == nil {
		return p
	}
	return ""
}

func (a *App) listPlugins() {
	ui.Info("Plugin dir: %s", a.cfg.PluginDir)
	found := false
	for _, f := range a.discoverPlugins() {
		name := strings.TrimPrefix(filepath.Base(f), "pssh-")
		ui.Status(ui.OK(), name, f)
		found = true
	}
	if !found {
		ui.Warn("No plugins found (install an executable named pssh-<name>).")
	}
}

func (a *App) discoverPlugins() []string {
	var out []string
	seen := map[string]bool{}
	dirs := append([]string{a.cfg.PluginDir}, filepath.SplitList(os.Getenv("PATH"))...)
	for _, d := range dirs {
		matches, _ := filepath.Glob(filepath.Join(d, "pssh-*"))
		for _, m := range matches {
			base := filepath.Base(m)
			if seen[base] || !isExec(m) {
				continue
			}
			seen[base] = true
			out = append(out, m)
		}
	}
	return out
}

// ---- doctor ---------------------------------------------------------------

func (a *App) doctor() int {
	rc := 0
	fmt.Printf("\033[34mpssh doctor\033[0m  (v%s)\n\n", Version)

	if ui.HasGum() {
		ui.Status(ui.OK(), "gum", firstLine(cmdOut("gum", "--version")))
	} else {
		ui.Status(ui.Warm(), "gum", "not found — pickers fall back to numbered prompts")
	}
	if _, err := exec.LookPath("passbolt"); err == nil {
		ui.Status(ui.OK(), "passbolt", firstLine(cmdOut("passbolt", "--version")))
	} else {
		ui.Status(ui.Bad(), "passbolt", "not found — install the passbolt CLI")
		rc = 1
	}
	if tool := clip.Detect(a.cfg.Clipboard); tool != nil {
		ui.Status(ui.OK(), "clipboard", strings.Join(tool, " "))
	} else {
		ui.Status(ui.Bad(), "clipboard", "none (wl-copy/xclip/xsel/pbcopy/clip.exe)")
		rc = 1
	}
	if _, err := os.Stat(a.cfg.SSHConfig); err == nil {
		ui.Status(ui.OK(), "ssh config", fmt.Sprintf("%s (%d hosts)", a.cfg.SSHConfig, len(a.ssh.Hosts())))
	} else {
		ui.Status(ui.Bad(), "ssh config", "not readable: "+a.cfg.SSHConfig)
		rc = 1
	}
	switch a.cfg.Store {
	case "ssh-comment", "passbolt-uri":
		ui.Status(ui.OK(), "store", a.cfg.Store)
	default:
		ui.Status(ui.Bad(), "store", "unknown PSSH_STORE="+a.cfg.Store)
		rc = 1
	}
	dm := a.deliverMethod()
	detail := dm + " (PSSH_DELIVER=" + a.cfg.Deliver + ")"
	if dm == "clipboard" && !sshSupportsAskpass() {
		detail += " — ssh too old for askpass"
	}
	ui.Status(ui.OK(), "delivery", detail)
	ui.Status(ui.OK(), "plugins", fmt.Sprintf("%d found (%s + PATH)", len(a.discoverPlugins()), a.cfg.PluginDir))

	if _, err := exec.LookPath("passbolt"); err == nil {
		if ui.HasGum() {
			ui.Info("Probing Passbolt…")
		}
		state, msg := a.pb.Probe()
		switch state {
		case passbolt.OK:
			ui.Status(ui.OK(), "passbolt auth", "reachable + authenticated")
		case passbolt.Unconfigured:
			ui.Status(ui.Warm(), "passbolt auth", "not configured")
			fmt.Println()
			a.passboltHelp(state, msg)
			rc = 1
		case passbolt.Unreachable:
			ui.Status(ui.Bad(), "passbolt auth", "server unreachable (DNS/network)")
			fmt.Println()
			a.passboltHelp(state, msg)
			rc = 1
		case passbolt.Auth:
			ui.Status(ui.Bad(), "passbolt auth", "configured but not authenticated")
			fmt.Println()
			a.passboltHelp(state, msg)
			rc = 1
		default:
			ui.Status(ui.Bad(), "passbolt auth", "error")
			fmt.Println()
			a.passboltHelp(state, msg)
			rc = 1
		}
	}

	fmt.Println()
	if rc == 0 {
		ui.Info("All good. pssh is ready.")
	} else {
		ui.Warn("Some checks need attention (see above).")
	}
	return rc
}

func (a *App) usage() {
	fmt.Fprintf(os.Stderr, `pssh — Passbolt-aware SSH launcher (v%s)

Usage:
  pssh                pick a host, then connect
  pssh <hostname>     connect straight to a host
  pssh link <host>    (re)assign the Passbolt resource for a host
  pssh unlink <host>  remove a host's association
  pssh <name> <host>  run plugin pssh-<name> with the host's password
  pssh <name>         run a plugin, picking the host first
  pssh plugins        list available plugins
  pssh doctor         check dependencies + passbolt connectivity
  pssh help           show this help

Options:
  --verbose, -V       trace resolution, matched resource + delivery (no secret)

Delivery (PSSH_DELIVER): auto | askpass | clipboard
Store    (PSSH_STORE):   ssh-comment | passbolt-uri
Config: %s
`, Version, filepath.Join(configDir(), "pssh", "config"))
}

// ---- helpers --------------------------------------------------------------

func firstNonOption(args []string) string {
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}
	return ""
}

func isExec(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode().Perm()&0o111 != 0
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func cmdOut(name string, args ...string) string {
	out, _ := exec.Command(name, args...).Output()
	return string(out)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	return filepath.Join(os.Getenv("HOME"), ".config")
}

// sshSupportsAskpass reports whether ssh is >= OpenSSH 8.4 (SSH_ASKPASS_REQUIRE).
func sshSupportsAskpass() bool {
	out, _ := exec.Command("ssh", "-V").CombinedOutput()
	m := regexp.MustCompile(`OpenSSH_(\d+)\.(\d+)`).FindStringSubmatch(string(out))
	if m == nil {
		return false
	}
	maj, _ := strconv.Atoi(m[1])
	min, _ := strconv.Atoi(m[2])
	return maj > 8 || (maj == 8 && min >= 4)
}

// ClipClear is the detached-helper entry point: sleep, then clear the clipboard.
func ClipClear(cfg config.Config, secs string) {
	n, err := strconv.Atoi(secs)
	if err != nil || n < 0 {
		return
	}
	time.Sleep(time.Duration(n) * time.Second)
	if tool := clip.Detect(cfg.Clipboard); tool != nil {
		_ = clip.Clear(tool)
	}
}
