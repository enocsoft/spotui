package library

import "testing"

const plTargetID = "plAAAAAAAAAAAAAAAAAAAA"
const alTargetID = "alAAAAAAAAAAAAAAAAAAAA"

// T25: :play accepts playlist and album contexts next to the track forms it
// always accepted — links (with /intl-xx/ segments, query strings, trailing
// slashes) and spotify: uris.
func TestParseTargetAcceptsContextForms(t *testing.T) {
	cases := []struct {
		in   string
		kind string
		id   string
	}{
		{"https://open.spotify.com/playlist/" + plTargetID, TargetPlaylist, plTargetID},
		{"https://open.spotify.com/playlist/" + plTargetID + "?si=abc123&nd=1", TargetPlaylist, plTargetID},
		{"https://open.spotify.com/intl-es/playlist/" + plTargetID, TargetPlaylist, plTargetID},
		{"http://open.spotify.com/playlist/" + plTargetID + "/", TargetPlaylist, plTargetID},
		{"open.spotify.com/playlist/" + plTargetID, TargetPlaylist, plTargetID},
		{"spotify:playlist:" + plTargetID, TargetPlaylist, plTargetID},
		{`  "` + "spotify:album:" + alTargetID + `"  `, TargetAlbum, alTargetID},
		{"https://open.spotify.com/album/" + alTargetID + "?si=xyz", TargetAlbum, alTargetID},
		{"https://open.spotify.com/intl-pt/album/" + alTargetID, TargetAlbum, alTargetID},
		{"spotify:album:" + alTargetID, TargetAlbum, alTargetID},
	}
	for _, c := range cases {
		got, err := ParseTarget(c.in)
		if err != nil {
			t.Errorf("ParseTarget(%q) = error %v, want %s %s", c.in, err, c.kind, c.id)
			continue
		}
		if got.Kind != c.kind || got.ID != c.id {
			t.Errorf("ParseTarget(%q) = %s %q, want %s %q", c.in, got.Kind, got.ID, c.kind, c.id)
		}
	}
}

// The track forms ParseTrackID accepts are exactly the track forms
// ParseTarget accepts — the track behaviour of :play is untouched (T25).
func TestParseTargetKeepsTheTrackForms(t *testing.T) {
	const id = "4uLU6hMCjMI75M1A2tKUQC"
	cases := []string{
		id,
		"  " + id + "  ",
		`"` + id + `"`,
		"spotify:track:" + id,
		"https://open.spotify.com/track/" + id,
		"https://open.spotify.com/track/" + id + "?si=abc123&nd=1",
		"https://open.spotify.com/intl-es/track/" + id + "?si=xyz",
		"open.spotify.com/track/" + id,
	}
	for _, in := range cases {
		got, err := ParseTarget(in)
		if err != nil {
			t.Errorf("ParseTarget(%q) = error %v, want track %q", in, err, id)
			continue
		}
		if got.Kind != TargetTrack || got.ID != id {
			t.Errorf("ParseTarget(%q) = %s %q, want track %q", in, got.Kind, got.ID, id)
		}
	}
}

func TestParseTargetRejectsGarbage(t *testing.T) {
	bad := []string{
		"",
		"t02", // mock row id
		"https://open.spotify.com/playlist/short-id",
		"spotify:album:4uLU6hMCjMI75M1A2tKUQ!",  // not base62
		"spotify:artist:4uLU6hMCjMI75M1A2tKUQC", // a kind the palette does not take
		"4uLU6hMCjMI75M1A2tKUQ",                 // 21 chars
		"4uLU6hMCjMI75M1A2tKUQCX",               // 23 chars
	}
	for _, in := range bad {
		if got, err := ParseTarget(in); err == nil {
			t.Errorf("ParseTarget(%q) = %s %q, want an error", in, got.Kind, got.ID)
		}
	}
}
