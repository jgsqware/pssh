package clip

import (
	"bytes"
	"testing"
)

func TestUTF16LE(t *testing.T) {
	// "Aé" → 'A'=0x41, 'é'=0x00E9, little-endian 16-bit units.
	got := utf16le("Aé")
	want := []byte{0x41, 0x00, 0xE9, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("utf16le(Aé) = % x, want % x", got, want)
	}
	if len(utf16le("")) != 0 {
		t.Fatal("empty string should encode to no bytes")
	}
}

func TestIsClipExe(t *testing.T) {
	cases := map[string]bool{
		"clip.exe":            true,
		"/mnt/c/.../clip.exe": true,
		"wl-copy":             false,
		"":                    false,
	}
	for tool, want := range cases {
		var argv []string
		if tool != "" {
			argv = []string{tool}
		}
		if got := IsClipExe(argv); got != want {
			t.Errorf("IsClipExe(%q) = %v, want %v", tool, got, want)
		}
	}
}

func TestDetectOverride(t *testing.T) {
	got := Detect("xclip -selection clipboard")
	want := []string{"xclip", "-selection", "clipboard"}
	if len(got) != len(want) {
		t.Fatalf("Detect override = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Detect override = %v, want %v", got, want)
		}
	}
}
