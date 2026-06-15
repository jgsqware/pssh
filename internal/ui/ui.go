// Package ui handles user-facing output and interactive pickers, preferring gum
// when available and falling back to plain numbered prompts otherwise.
package ui

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// ErrCancelled is returned when the user aborts a picker.
var ErrCancelled = errors.New("cancelled")

const (
	cReset  = "\033[0m"
	cRed    = "\033[31m"
	cGreen  = "\033[32m"
	cYellow = "\033[33m"
	cBlue   = "\033[34m"
	cDim    = "\033[2m"
)

var verbose bool

// SetVerbose toggles [pssh] trace lines.
func SetVerbose(v bool) { verbose = v }

func tty() bool {
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func paint(color, s string) string {
	if !tty() {
		return s
	}
	return color + s + cReset
}

// Info, Warn, Err, Vlog write status lines to stderr (never stdout).
func Info(format string, a ...any) { fmt.Fprintln(os.Stderr, paint(cBlue, fmt.Sprintf(format, a...))) }
func Warn(format string, a ...any) {
	fmt.Fprintln(os.Stderr, paint(cYellow, fmt.Sprintf(format, a...)))
}
func Err(format string, a ...any) { fmt.Fprintln(os.Stderr, paint(cRed, fmt.Sprintf(format, a...))) }

func Vlog(format string, a ...any) {
	if verbose {
		fmt.Fprintln(os.Stderr, paint(cDim, "[pssh] ")+fmt.Sprintf(format, a...))
	}
}

// HasGum reports whether the gum binary is available.
func HasGum() bool { _, err := exec.LookPath("gum"); return err == nil }

// Box prints a highlighted confirmation (gum style, or plain) to stderr.
func Box(format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	if HasGum() {
		cmd := exec.Command("gum", "style", "--foreground", "42",
			"--border", "rounded", "--padding", "0 1", msg)
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		if cmd.Run() == nil {
			return
		}
	}
	Info("%s", msg)
}

// Pick lets the user choose one of items. The returned value is exactly one of
// the input strings. Returns ErrCancelled on abort.
func Pick(items []string, placeholder string) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("nothing to pick")
	}
	if HasGum() {
		cmd := exec.Command("gum", "filter", "--placeholder", placeholder, "--height", "15")
		cmd.Stdin = strings.NewReader(strings.Join(items, "\n"))
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return "", ErrCancelled
		}
		choice := strings.TrimRight(string(out), "\r\n")
		if choice == "" {
			return "", ErrCancelled
		}
		return choice, nil
	}
	return pickNumbered(items)
}

// pickNumbered is the no-gum fallback: a validated numeric selection.
func pickNumbered(items []string) (string, error) {
	for i, it := range items {
		fmt.Fprintf(os.Stderr, "%3d  %s\n", i+1, it)
	}
	fmt.Fprint(os.Stderr, "Number: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return "", ErrCancelled
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil || n < 1 || n > len(items) {
		return "", fmt.Errorf("invalid selection")
	}
	return items[n-1], nil
}

// Marks for doctor lines.
func OK() string   { return paint(cGreen, "✓") }
func Bad() string  { return paint(cRed, "✗") }
func Warm() string { return paint(cYellow, "!") }

// Status prints a "  <mark> <label> — <detail>" doctor line to stdout.
func Status(mark, label, detail string) {
	if detail != "" {
		fmt.Printf("  %s %s %s\n", mark, label, paint(cDim, "— "+detail))
	} else {
		fmt.Printf("  %s %s\n", mark, label)
	}
}
