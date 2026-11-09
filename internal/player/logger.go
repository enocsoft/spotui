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
	"strings"

	librespot "github.com/elxgy/go-librespot"
)

const loginLinkPrefix = "to complete authentication visit the following link: "

type captureLogger struct {
	w       io.Writer
	fields  string
	onLogin func(url string)
}

func newCaptureLogger(onLogin func(url string)) *captureLogger {
	return &captureLogger{w: os.Stderr, onLogin: onLogin}
}

func (l *captureLogger) writef(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if strings.HasPrefix(msg, loginLinkPrefix) && l.onLogin != nil {
		l.onLogin(strings.TrimPrefix(msg, loginLinkPrefix))
	}
	if l.fields != "" {
		msg = l.fields + " " + msg
	}
	fmt.Fprintf(l.w, "spotify-tui: %s\n", msg)
}

func (l *captureLogger) Tracef(format string, args ...any) {}
func (l *captureLogger) Debugf(format string, args ...any) {}
func (l *captureLogger) Infof(format string, args ...any)  { l.writef(format, args...) }
func (l *captureLogger) Warnf(format string, args ...any)  { l.writef("warning: "+format, args...) }
func (l *captureLogger) Errorf(format string, args ...any) { l.writef(format, args...) }

func (l *captureLogger) Trace(args ...any) {}
func (l *captureLogger) Debug(args ...any) {}
func (l *captureLogger) Info(args ...any)  { l.writef("%v", args...) }
func (l *captureLogger) Warn(args ...any)  { l.writef("warning: %v", args...) }
func (l *captureLogger) Error(args ...any) { l.writef("%v", args...) }

func (l *captureLogger) WithField(key string, value any) librespot.Logger {
	return &captureLogger{w: l.w, fields: l.fields + " " + fmt.Sprintf("%s=%v", key, value), onLogin: l.onLogin}
}

func (l *captureLogger) WithError(err error) librespot.Logger {
	return &captureLogger{w: l.w, fields: l.fields + " error=" + err.Error(), onLogin: l.onLogin}
}
