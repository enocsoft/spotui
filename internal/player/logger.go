// captureLogger routes go-librespot logs to stderr with a fixed prefix and
// watches for the interactive-login URL line, surfacing it as a player event
// instead of leaving the TUI hanging silently (spec S9). It never logs
// credentials: go-librespot only emits the auth URL (a challenge, not a
// secret) and obfuscated usernames at this level.
package player

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	librespot "github.com/elxgy/go-librespot"
)

const loginLinkPrefix = "to complete authentication visit the following link: "

type captureLogger struct {
	w       io.Writer
	fields  string
	onLogin func(url string)
}

func newCaptureLogger(stateDir string, onLogin func(url string)) *captureLogger {
	w := io.Writer(os.Stderr)
	// bubbletea owns the screen: writing to stderr from here corrupts the frame and
	// pushes log lines into the UI (it showed up as stray "ctrl+c quit" lines over the
	// view). The file is the only sink while the TUI runs; stderr is the fallback for
	// a run with no state dir.
	if stateDir != "" {
		if err := os.MkdirAll(stateDir, 0o700); err == nil {
			if f, err := os.OpenFile(filepath.Join(stateDir, "player.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
				w = f
			}
		}
	}
	return &captureLogger{w: w, onLogin: onLogin}
}

// longSecret matches token-shaped runs. go-librespot logs whole client tokens and
// access points at debug level; a debug log that leaks credentials is a liability,
// and the authorization URL has its own file where it stays readable.
var longSecret = regexp.MustCompile(`[A-Za-z0-9+/=_-]{40,}`)

func (l *captureLogger) writef(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	// Hand the URL over before redaction: it is not a secret (a challenge and a
	// state, no token) and the UI has to show it whole. Redaction is for the file.
	if strings.HasPrefix(msg, loginLinkPrefix) && l.onLogin != nil {
		l.onLogin(strings.TrimPrefix(msg, loginLinkPrefix))
	}
	msg = longSecret.ReplaceAllString(msg, "<redacted>")
	if l.fields != "" {
		msg = l.fields + " " + msg
	}
	fmt.Fprintf(l.w, "spotify-tui: %s\n", msg)
}

func (l *captureLogger) Tracef(format string, args ...any) { l.writef("trace: "+format, args...) }
func (l *captureLogger) Debugf(format string, args ...any) { l.writef(format, args...) }
func (l *captureLogger) Infof(format string, args ...any)  { l.writef(format, args...) }
func (l *captureLogger) Warnf(format string, args ...any)  { l.writef("warning: "+format, args...) }
func (l *captureLogger) Errorf(format string, args ...any) { l.writef(format, args...) }

func (l *captureLogger) Trace(args ...any) { l.writef("trace: %v", args...) }
func (l *captureLogger) Debug(args ...any) { l.writef("%v", args...) }
func (l *captureLogger) Info(args ...any)  { l.writef("%v", args...) }
func (l *captureLogger) Warn(args ...any)  { l.writef("warning: %v", args...) }
func (l *captureLogger) Error(args ...any) { l.writef("%v", args...) }

func (l *captureLogger) WithField(key string, value any) librespot.Logger {
	return &captureLogger{w: l.w, fields: l.fields + " " + fmt.Sprintf("%s=%v", key, value), onLogin: l.onLogin}
}

func (l *captureLogger) WithError(err error) librespot.Logger {
	return &captureLogger{w: l.w, fields: l.fields + " error=" + err.Error(), onLogin: l.onLogin}
}
