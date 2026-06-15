// Package sshcfg parses an OpenSSH client config (with Include expansion) to
// list hosts and to read/write pssh's `# pssh: <id>` association comments.
package sshcfg

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// maxIncludeDepth matches OpenSSH's own Include nesting cap and guards against
// circular Includes (a self/mutual reference would otherwise recurse forever).
const maxIncludeDepth = 16

var psshCommentRe = regexp.MustCompile(`#[[:space:]]*pssh:[[:space:]]*([^[:space:]]+)`)

// Config points at a root ssh config file.
type Config struct{ Root string }

func New(root string) *Config { return &Config{Root: root} }

// stripComment removes an inline `#...` comment from a config line.
func stripComment(line string) string {
	if i := strings.IndexByte(line, '#'); i >= 0 {
		return line[:i]
	}
	return line
}

// keyword returns the lowercased first token of a config line, and the rest.
func keyword(line string) (string, []string) {
	fields := strings.Fields(stripComment(line))
	if len(fields) == 0 {
		return "", nil
	}
	return strings.ToLower(fields[0]), fields[1:]
}

func isWildcard(tok string) bool { return strings.ContainsAny(tok, "*?!") }

// Files returns the root config plus every file pulled in via Include, in
// depth-first order, de-duplicated, with a recursion guard.
func (c *Config) Files() []string {
	var out []string
	seen := map[string]bool{}
	c.expand(c.Root, 0, seen, &out)
	return out
}

func (c *Config) expand(path string, depth int, seen map[string]bool, out *[]string) {
	if depth > maxIncludeDepth {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if seen[abs] {
		return
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		return
	}
	seen[abs] = true
	*out = append(*out, path)

	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	dir := filepath.Dir(path)
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		kw, rest := keyword(sc.Text())
		if kw != "include" {
			continue
		}
		for _, pat := range rest {
			var glob string
			switch {
			case strings.HasPrefix(pat, "/"):
				glob = pat
			case strings.HasPrefix(pat, "~/"):
				glob = filepath.Join(os.Getenv("HOME"), pat[2:])
			default:
				glob = filepath.Join(dir, pat)
			}
			matches, _ := filepath.Glob(glob)
			for _, m := range matches {
				c.expand(m, depth+1, seen, out)
			}
		}
	}
}

// Hosts lists every concrete (non-wildcard) Host alias across all files,
// de-duplicated in first-seen order.
func (c *Config) Hosts() []string {
	var out []string
	seen := map[string]bool{}
	for _, file := range c.Files() {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			kw, rest := keyword(sc.Text())
			if kw != "host" {
				continue
			}
			for _, tok := range rest {
				if isWildcard(tok) || seen[tok] {
					continue
				}
				seen[tok] = true
				out = append(out, tok)
			}
		}
		f.Close()
	}
	return out
}

// CommentID returns the `# pssh:` resource id recorded in host's block, if any.
func (c *Config) CommentID(host string) string {
	for _, file := range c.Files() {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		inblock := false
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			raw := sc.Text()
			kw, rest := keyword(raw)
			if kw == "host" || kw == "match" {
				inblock = kw == "host" && contains(rest, host)
				continue
			}
			if inblock {
				if m := psshCommentRe.FindStringSubmatch(raw); m != nil {
					f.Close()
					return m[1]
				}
			}
		}
		f.Close()
	}
	return ""
}

// BlockFile returns the config file containing host's Host block.
func (c *Config) BlockFile(host string) string {
	for _, file := range c.Files() {
		f, err := os.Open(file)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		found := false
		for sc.Scan() {
			kw, rest := keyword(sc.Text())
			if kw == "host" && contains(rest, host) {
				found = true
				break
			}
		}
		f.Close()
		if found {
			return file
		}
	}
	return ""
}

// WriteComment inserts/replaces `# pssh: <id>` in host's block. The write is
// atomic (temp + rename) and follows symlinks so chezmoi-managed config.d files
// keep their link.
func (c *Config) WriteComment(host, id string) error {
	file := c.BlockFile(host)
	if file == "" {
		return fmt.Errorf("%q is not a Host in your ssh config; add it, or set PSSH_STORE=passbolt-uri", host)
	}
	lines, err := readLines(file)
	if err != nil {
		return err
	}
	var out []string
	inblock := false
	for _, raw := range lines {
		kw, rest := keyword(raw)
		if kw == "host" || kw == "match" {
			inblock = false
			out = append(out, raw)
			if kw == "host" && contains(rest, host) {
				inblock = true
				out = append(out, "    # pssh: "+id)
			}
			continue
		}
		if inblock && psshCommentRe.MatchString(raw) {
			continue // drop the old association
		}
		out = append(out, raw)
	}
	return writeAtomic(file, out)
}

// RemoveComment drops `# pssh:` from host's block. Reports whether it removed one.
func (c *Config) RemoveComment(host string) (bool, error) {
	if c.CommentID(host) == "" {
		return false, nil
	}
	file := c.BlockFile(host)
	if file == "" {
		return false, nil
	}
	lines, err := readLines(file)
	if err != nil {
		return false, err
	}
	var out []string
	inblock := false
	for _, raw := range lines {
		kw, rest := keyword(raw)
		if kw == "host" || kw == "match" {
			inblock = kw == "host" && contains(rest, host)
			out = append(out, raw)
			continue
		}
		if inblock && psshCommentRe.MatchString(raw) {
			continue
		}
		out = append(out, raw)
	}
	if err := writeAtomic(file, out); err != nil {
		return false, err
	}
	return true, nil
}

// ResolvedHostName returns the HostName ssh would use for host (via `ssh -G`),
// or "" if it can't be determined or equals the alias.
func (c *Config) ResolvedHostName(host string) string {
	out, err := exec.Command("ssh", "-G", host).Output()
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) >= 2 && f[0] == "hostname" {
			if f[1] != host {
				return f[1]
			}
			return ""
		}
	}
	return ""
}

func contains(ss []string, want string) bool { return slices.Contains(ss, want) }

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return lines, sc.Err()
}

// writeAtomic writes lines to path's real target (resolving symlinks) via a
// temp file + rename, preserving the file mode.
func writeAtomic(path string, lines []string) error {
	target := path
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		target = resolved
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	dir := filepath.Dir(target)
	tmp, err := os.CreateTemp(dir, ".pssh-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	w := bufio.NewWriter(tmp)
	for _, l := range lines {
		if _, err := w.WriteString(l + "\n"); err != nil {
			tmp.Close()
			return err
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, target)
}
