package app

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/enocsoft/spotui/internal/config"
)

// debugf appends to <state>/debug.log only when SPOTUI_DEBUG is set. It
// exists because the interesting failures happen in the state machine, not on
// screen: writing to stderr would corrupt the frame, and guessing costs more
// than one log line per tick.
func debugf(format string, args ...any) {
	if os.Getenv("SPOTUI_DEBUG") == "" && os.Getenv("SPOTIFY_TUI_DEBUG") == "" {
		return
	}
	dir := config.StateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "debug.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}
