// Command pssh is a Passbolt-aware ssh launcher: pick a host from your ssh
// config, resolve the linked Passbolt password, and authenticate automatically.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/jgsqware/pssh/internal/app"
	"github.com/jgsqware/pssh/internal/config"
)

func main() {
	// SSH_ASKPASS mode: ssh exec's this binary with the prompt as os.Args[1].
	// Answer password/passphrase prompts with the secret; decline anything else
	// (host-key "yes/no", empty prompts) so we never auto-trust or misdeliver.
	if os.Getenv("PSSH_ASKPASS") == "1" {
		prompt := ""
		if len(os.Args) > 1 {
			prompt = os.Args[1]
		}
		if isPasswordPrompt(prompt) {
			fmt.Println(os.Getenv("PSSH_ASKPASS_VALUE"))
			os.Exit(0)
		}
		os.Exit(1)
	}

	// Detached clipboard-clear helper (spawned before exec for auto-clear).
	if len(os.Args) > 1 && os.Args[1] == "__clipclear" {
		secs := ""
		if len(os.Args) > 2 {
			secs = os.Args[2]
		}
		app.ClipClear(config.Load(), secs)
		return
	}

	os.Exit(app.New(config.Load()).Run(os.Args[1:]))
}

// isPasswordPrompt matches genuine password/passphrase prompts only. An empty
// prompt is explicitly NOT a match (it must not coax the secret out).
func isPasswordPrompt(p string) bool {
	if p == "" {
		return false
	}
	lp := strings.ToLower(p)
	return strings.Contains(lp, "password") || strings.Contains(lp, "passphrase")
}
