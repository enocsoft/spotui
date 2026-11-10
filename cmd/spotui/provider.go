package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/nahuel/spotui/internal/config"
)

// clientIDPattern is what a Spotify app client id looks like: 32 hex chars.
// Validating here turns a confusing runtime failure into a clear message.
var clientIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// runProvider is `spotui --provider`: ask for the Spotify app client id, check
// it, and store it in the config so the first real run has what it needs.
// Input is a reader so a piped value works without a TTY.
func runProvider(in io.Reader, out io.Writer) error {
	current := ""
	if cfg, err := config.Load(); err == nil {
		current = cfg.ClientID
	}
	if current != "" {
		fmt.Fprintf(out, "current client id: %s\n", maskClientID(current))
	}
	fmt.Fprint(out, "Spotify app client id (dashboard → your app → Settings): ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && line == "" {
		return fmt.Errorf("spotui: no client id given")
	}
	id := strings.Trim(strings.TrimSpace(line), `"'`)
	if id == "" {
		if current != "" {
			fmt.Fprintln(out, "kept the current client id")
			return nil
		}
		return fmt.Errorf("spotui: no client id given")
	}
	if !clientIDPattern.MatchString(id) {
		return fmt.Errorf("spotui: %q is not a client id · expected 32 hex characters", id)
	}
	path, err := config.SaveClientID(id)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "saved to %s\n", path)
	fmt.Fprintln(out, "next: run 'spotui' · it opens the Spotify authorization page in your browser")
	return nil
}

func maskClientID(id string) string {
	if len(id) < 8 {
		return "…"
	}
	return id[:3] + "…" + id[len(id)-2:]
}
