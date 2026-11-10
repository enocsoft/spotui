package app

import (
	"os/exec"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"
)

// openInBrowser launches the platform opener with the authorization URL as one
// argv entry. No shell: the URL comes from the provider and must never be
// re-parsed by a shell, and there is nothing to quote.
//
// It is a variable so tests can assert what would be launched without opening a
// browser during `go test`.
var openInBrowser = func(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

// browserOpenedMsg reports the outcome of the opener so the UI can say what
// happened instead of leaving a key that did nothing.
type browserOpenedMsg struct {
	url string
	err error
}

func openBrowserCmd(rawURL string) tea.Cmd {
	return func() tea.Msg { return browserOpenedMsg{url: rawURL, err: openInBrowser(rawURL)} }
}
