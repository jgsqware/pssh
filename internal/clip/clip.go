// Package clip copies text to the system clipboard. On WSL, clip.exe reads its
// input in the Windows codepage and corrupts UTF-8 multibyte characters, so we
// transcode to UTF-16LE for it natively (no external iconv needed).
package clip

import (
	"encoding/binary"
	"os/exec"
	"strings"
	"unicode/utf16"
)

// Detect returns the clipboard command (argv) to use, or nil if none is found.
// override, when non-empty, wins (split on whitespace).
func Detect(override string) []string {
	if override != "" {
		return strings.Fields(override)
	}
	candidates := [][]string{
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
		{"xsel", "-b"},
		{"pbcopy"},
		{"clip.exe"},
	}
	for _, c := range candidates {
		if _, err := exec.LookPath(c[0]); err == nil {
			return c
		}
	}
	return nil
}

// IsClipExe reports whether the tool is Windows' clip.exe (needs UTF-16LE).
func IsClipExe(tool []string) bool {
	if len(tool) == 0 {
		return false
	}
	return strings.HasSuffix(tool[0], "clip.exe")
}

// Copy writes data to the clipboard using tool, transcoding for clip.exe.
func Copy(tool []string, data string) error {
	if len(tool) == 0 {
		return exec.ErrNotFound
	}
	cmd := exec.Command(tool[0], tool[1:]...)
	if IsClipExe(tool) {
		cmd.Stdin = strings.NewReader(string(utf16le(data)))
	} else {
		cmd.Stdin = strings.NewReader(data)
	}
	return cmd.Run()
}

// Clear empties the clipboard.
func Clear(tool []string) error { return Copy(tool, "") }

// utf16le encodes s as little-endian UTF-16 (no BOM), which clip.exe accepts
// losslessly.
func utf16le(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, len(u)*2)
	for i, r := range u {
		binary.LittleEndian.PutUint16(b[i*2:], r)
	}
	return b
}
