package sshcfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture builds a small ssh config tree with an Include and returns the
// root path.
func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "config")
	mustWrite(t, root, "Include config.d/*\n\nHost bastion\n    HostName 10.0.0.1\n\nHost *.internal\n    User svc\n")
	mustWrite(t, filepath.Join(dir, "config.d", "prod"),
		"Host web01 web01.example.com\n    HostName 192.168.1.10\n    # pssh: abc-123\n\nHost db01\n    HostName 192.168.1.20\n")
	return root
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHostsAndIncludes(t *testing.T) {
	c := New(writeFixture(t))
	got := c.Hosts()
	want := []string{"bastion", "web01", "web01.example.com", "db01"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Hosts() = %v, want %v", got, want)
	}
}

func TestCommentID(t *testing.T) {
	c := New(writeFixture(t))
	if got := c.CommentID("web01"); got != "abc-123" {
		t.Fatalf("CommentID(web01) = %q, want abc-123", got)
	}
	if got := c.CommentID("db01"); got != "" {
		t.Fatalf("CommentID(db01) = %q, want empty", got)
	}
}

func TestWriteReplaceRemoveComment(t *testing.T) {
	root := writeFixture(t)
	c := New(root)

	if err := c.WriteComment("db01", "new-999"); err != nil {
		t.Fatal(err)
	}
	if got := c.CommentID("db01"); got != "new-999" {
		t.Fatalf("after write, CommentID(db01) = %q", got)
	}

	// Replace existing (no duplicate).
	if err := c.WriteComment("web01", "updated-456"); err != nil {
		t.Fatal(err)
	}
	if got := c.CommentID("web01"); got != "updated-456" {
		t.Fatalf("after replace, CommentID(web01) = %q", got)
	}
	data, _ := os.ReadFile(c.BlockFile("web01"))
	if n := strings.Count(string(data), "# pssh:"); n != 2 { // web01 + db01
		t.Fatalf("expected 2 pssh comments in file, got %d:\n%s", n, data)
	}

	// Remove.
	removed, err := c.RemoveComment("web01")
	if err != nil || !removed {
		t.Fatalf("RemoveComment(web01) = %v, %v", removed, err)
	}
	if got := c.CommentID("web01"); got != "" {
		t.Fatalf("after remove, CommentID(web01) = %q", got)
	}
	if got := c.CommentID("db01"); got != "new-999" {
		t.Fatalf("db01 disturbed by web01 removal: %q", got)
	}
	if removed, _ := c.RemoveComment("web01"); removed {
		t.Fatal("second RemoveComment should report nothing removed")
	}
}

func TestWritePreservesSymlink(t *testing.T) {
	root := writeFixture(t)
	c := New(root)
	dir := filepath.Dir(root)

	// Replace config.d/prod with a symlink to a target elsewhere.
	target := filepath.Join(dir, "real-prod")
	link := filepath.Join(dir, "config.d", "prod")
	data, _ := os.ReadFile(link)
	os.Remove(link)
	mustWrite(t, target, string(data))
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	if err := c.WriteComment("db01", "sym-1"); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink was replaced by a regular file")
	}
	if c.CommentID("db01") != "sym-1" {
		t.Fatal("write did not reach the symlink target")
	}
}

func TestCircularIncludeTerminates(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	mustWrite(t, a, "Include b\nHost ha\n")
	mustWrite(t, b, "Include a\nHost hb\n")
	c := New(a)
	// Must terminate (recursion guard) and visit each file once.
	files := c.Files()
	if len(files) != 2 {
		t.Fatalf("Files() = %v, want 2 unique files", files)
	}
}
