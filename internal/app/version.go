package app

import "runtime/debug"

// Revision reports the VCS revision the binary was built from, shortened to
// seven characters and marked when the worktree had local changes. It returns
// "dev" when the build carries no VCS metadata.
//
// It is rendered on the login and error screens on purpose: a binary built
// before the PKCE login once sent a live debugging session down the wrong path,
// because the URL it printed came from a flow the server had already retired.
func Revision() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var rev string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	if dirty {
		rev += "+dirty"
	}
	return rev
}
