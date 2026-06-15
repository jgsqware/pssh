// Package config loads pssh settings from the environment and an optional
// ~/.config/pssh/config file (simple KEY=VALUE lines, like the shell version).
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Config holds all tunables. Fields mirror the PSSH_* environment variables.
type Config struct {
	Store        string // ssh-comment | passbolt-uri
	Deliver      string // auto | askpass | clipboard
	Clear        string // clipboard auto-clear seconds; "" or "0" = off
	Clipboard    string // explicit clipboard command override
	PassboltArgs string // extra flags for every passbolt call
	ProbeTimeout string // e.g. "10s"
	SSHConfig    string // path to ssh config
	PluginDir    string // where pssh-<name> plugins live
	Verbose      bool
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return x
	}
	return filepath.Join(os.Getenv("HOME"), ".config")
}

// Load reads the config file (if present) then overlays environment variables.
func Load() Config {
	home := os.Getenv("HOME")
	path := envOr("PSSH_CONFIG", filepath.Join(configDir(), "pssh", "config"))
	fileVals := readKV(path)

	get := func(key, def string) string {
		if v, ok := os.LookupEnv(key); ok {
			return v
		}
		if v, ok := fileVals[key]; ok {
			return v
		}
		return def
	}

	c := Config{
		Store:        get("PSSH_STORE", "ssh-comment"),
		Deliver:      get("PSSH_DELIVER", "auto"),
		Clear:        get("PSSH_CLEAR", ""),
		Clipboard:    get("PSSH_CLIPBOARD", ""),
		PassboltArgs: get("PSSH_PASSBOLT_FLAGS", ""),
		ProbeTimeout: get("PSSH_PROBE_TIMEOUT", "10s"),
		SSHConfig:    get("PSSH_SSH_CONFIG", filepath.Join(home, ".ssh", "config")),
		PluginDir:    get("PSSH_PLUGIN_DIR", filepath.Join(configDir(), "pssh", "plugins")),
		Verbose:      get("PSSH_VERBOSE", "0") == "1",
	}
	return c
}

// ClearActive reports whether clipboard auto-clear is enabled.
func (c Config) ClearActive() bool { return c.Clear != "" && c.Clear != "0" }

// readKV parses simple KEY=VALUE lines, ignoring blanks, comments, and an
// optional leading `export `. Quotes around the value are stripped.
func readKV(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		out[k] = v
	}
	return out
}
