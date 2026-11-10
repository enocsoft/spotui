package library

import "testing"

func TestParseTrackIDAcceptsPastedForms(t *testing.T) {
	const id = "4uLU6hMCjMI75M1A2tKUQC"
	cases := []string{
		id,
		"  " + id + "  ",
		`"` + id + `"`,
		"spotify:track:" + id,
		"https://open.spotify.com/track/" + id,
		"https://open.spotify.com/track/" + id + "?si=abc123&nd=1",
		"https://open.spotify.com/intl-es/track/" + id + "?si=xyz",
		"http://open.spotify.com/track/" + id + "/",
		"open.spotify.com/track/" + id,
	}
	for _, in := range cases {
		got, err := ParseTrackID(in)
		if err != nil {
			t.Errorf("ParseTrackID(%q) = error %v, want %q", in, err, id)
			continue
		}
		if got != id {
			t.Errorf("ParseTrackID(%q) = %q, want %q", in, got, id)
		}
	}
}

func TestParseTrackIDRejectsTheRest(t *testing.T) {
	bad := []string{
		"",
		"t02", // mock row id
		"https://open.spotify.com/playlist/4uLU6hMCjMI75M1A2tKUQC",
		"https://open.spotify.com/album/4uLU6hMCjMI75M1A2tKUQC",
		"spotify:album:4uLU6hMCjMI75M1A2tKUQC",
		"4uLU6hMCjMI75M1A2tKUQ",   // 21 chars
		"4uLU6hMCjMI75M1A2tKUQCX", // 23 chars
		"4uLU6hMCjMI75M1A2tKUQ!",  // not base62
	}
	for _, in := range bad {
		if got, err := ParseTrackID(in); err == nil {
			t.Errorf("ParseTrackID(%q) = %q, want an error", in, got)
		}
	}
}
