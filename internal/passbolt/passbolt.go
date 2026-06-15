// Package passbolt wraps the `passbolt` CLI. It caches the resource list for the
// life of the process so a single run authenticates at most twice (list + get).
package passbolt

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
)

// Resource is one Passbolt entry (lowercase JSON keys, as the CLI emits).
type Resource struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Username string `json:"username"`
	URI      string `json:"uri"`
}

// State classifies a connectivity probe result.
type State int

const (
	OK State = iota
	Unconfigured
	Unreachable
	Auth
	Error
)

func (s State) String() string {
	switch s {
	case OK:
		return "ok"
	case Unconfigured:
		return "unconfigured"
	case Unreachable:
		return "unreachable"
	case Auth:
		return "auth"
	default:
		return "error"
	}
}

// Client invokes the passbolt CLI with shared flags and a list cache.
type Client struct {
	flags   []string
	timeout string

	cached bool
	list   []Resource
}

// New builds a client. extraFlags is the raw PSSH_PASSBOLT_FLAGS string.
func New(extraFlags, timeout string) *Client {
	return &Client{flags: strings.Fields(extraFlags), timeout: timeout}
}

func (c *Client) run(args ...string) (stdout, stderr []byte, err error) {
	full := append(append([]string{}, c.flags...), args...)
	cmd := exec.Command("passbolt", full...)
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	err = cmd.Run()
	return out.Bytes(), errBuf.Bytes(), err
}

// List returns all resources visible to the user, cached after the first call.
func (c *Client) List() ([]Resource, error) {
	if c.cached {
		return c.list, nil
	}
	out, _, err := c.run("list", "resource", "-j", "--timeout", c.timeout,
		"-c", "ID", "-c", "Name", "-c", "Username", "-c", "URI")
	if err != nil {
		return nil, err
	}
	var rs []Resource
	if err := json.Unmarshal(out, &rs); err != nil {
		return nil, err
	}
	c.list, c.cached = rs, true
	return rs, nil
}

// FindByURI returns every cached resource whose URI equals uri.
func (c *Client) FindByURI(uri string) ([]Resource, error) {
	rs, err := c.List()
	if err != nil {
		return nil, err
	}
	var out []Resource
	for _, r := range rs {
		if r.URI == uri {
			out = append(out, r)
		}
	}
	return out, nil
}

// Password fetches and decrypts the password for one resource id.
func (c *Client) Password(id string) (string, error) {
	out, _, err := c.run("get", "resource", "--id", id, "-j")
	if err != nil {
		return "", err
	}
	var v struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(out, &v); err != nil {
		return "", err
	}
	return v.Password, nil
}

// SetURI updates a resource's URI (used by the passbolt-uri store).
func (c *Client) SetURI(id, uri string) error {
	_, _, err := c.run("update", "resource", "--id", id, "--uri", uri)
	c.cached = false // invalidate cache after a mutation
	return err
}

// Probe checks connectivity and classifies any failure. The returned message is
// the raw CLI stderr (for display). A successful probe also warms the cache.
func (c *Client) Probe() (State, string) {
	out, errBuf, err := c.run("list", "resource", "-j", "--timeout", c.timeout, "-c", "ID")
	if err == nil {
		// Warm the metadata cache opportunistically (ignore parse errors).
		var rs []Resource
		if json.Unmarshal(out, &rs) == nil {
			c.list, c.cached = rs, true
		}
		return OK, ""
	}
	msg := string(errBuf)
	return classify(msg), msg
}

// classify maps a passbolt error message to a State. Order matters: the error
// text embeds the request URL (".../auth/verify.json"), so bare "auth"/"verify"
// substrings are unreliable — check config, then network, then real auth.
func classify(msg string) State {
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(msg, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("serverAddress", "not configured", "no config", "config file", "userPrivateKey"):
		return Unconfigured
	case has("no such host", "dial tcp", "connection refused", "deadline exceeded",
		"i/o timeout", "timeout", "TLS handshake", "x509", "certificate"):
		return Unreachable
	case has("401", "403", "Unauthorized", "Forbidden", "MFA", "passphrase", "authentication failed"):
		return Auth
	default:
		return Error
	}
}
