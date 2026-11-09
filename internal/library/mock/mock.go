// Package mock implements the library.DataSource and library.Player
// interfaces with in-memory mock data (spec S10: slice 1 has no network and
// no real Spotify calls). The data mirrors design/spotify-tui/01-fullscreen-list.html.
package mock

import (
	"errors"
	"fmt"
	"time"

	"github.com/nahuel/spotify-tui/internal/library"
)

// Mock is the in-memory backing store. It is safe for single-goroutine use
// (the bubbletea update loop).
type Mock struct {
	playlist library.Playlist
	queue    []library.Track
	session  library.Session
	sinks    []library.Sink
	sections []library.Section
}

// New builds the mock library: a 42-track playlist, a 41-item queue, a live
// local player session and the library sections.
func New() *Mock {
	tracks := make([]library.Track, 0, 42)
	for i, d := range trackSeed {
		tracks = append(tracks, library.Track{
			ID:       fmt.Sprintf("t%02d", i+1),
			Title:    d.title,
			Artist:   d.artist,
			Album:    d.album,
			Duration: d.dur(),
			Added:    d.added,
			Saved:    d.saved,
		})
	}
	queue := make([]library.Track, 0, 41)
	for i := 0; i < 41; i++ {
		queue = append(queue, tracks[(i+1)%42]) // up next, starting with Moanin'
	}
	return &Mock{
		playlist: library.Playlist{
			Name:    "Bebop Essentials",
			Owner:   "nahuel",
			Private: true,
			Tracks:  tracks,
		},
		queue: queue,
		session: library.Session{
			State:    library.StatePlaying,
			Output:   "Speakers · PipeWire",
			Bitrate:  "192 kbps",
			TrackID:  "t01", // So What
			Elapsed:  42 * time.Second,
			Total:    9*time.Minute + 22*time.Second,
			Volume:   72,
			Shuffle:  false,
			Repeat:   "playlist",
			QueueLen: 41,
		},
		sinks: []library.Sink{
			{Name: "Speakers · PipeWire", Active: true},
			{Name: "HDMI / DisplayPort"},
			{Name: "JACK"},
		},
		sections: []library.Section{
			{Name: "PLAYLISTS", Count: 12},
			{Name: "ALBUMS", Count: 84},
			{Name: "ARTISTS", Count: 61},
			{Name: "GENRES", Count: 14},
		},
	}
}

func (m *Mock) Library() []library.Section { return m.sections }

func (m *Mock) Playlist() library.Playlist { return m.playlist }

func (m *Mock) Queue() []library.Track { return m.queue }

func (m *Mock) Session() library.Session { return m.session }

func (m *Mock) Sinks() []library.Sink { return m.sinks }

func (m *Mock) SetSink(name string) error {
	for i := range m.sinks {
		if m.sinks[i].Name == name {
			for j := range m.sinks {
				m.sinks[j].Active = j == i
			}
			m.session.Output = name
			if m.session.State == library.StateNoOutput {
				m.session.State = library.StatePlaying
			}
			return nil
		}
	}
	return fmt.Errorf("no audio output %q", name)
}

func (m *Mock) SetSaved(id string, saved bool) error {
	for i := range m.playlist.Tracks {
		if m.playlist.Tracks[i].ID == id {
			m.playlist.Tracks[i].Saved = saved
			return nil
		}
	}
	return fmt.Errorf("no track %q in library", id)
}

func (m *Mock) Play(track library.Track) error {
	m.session.State = library.StatePlaying
	m.session.TrackID = track.ID
	m.session.Elapsed = 0
	m.session.Total = track.Duration
	return nil
}

func (m *Mock) TogglePause() error {
	switch m.session.State {
	case library.StatePlaying:
		m.session.State = library.StatePaused
	case library.StatePaused:
		m.session.State = library.StatePlaying
	default:
		return errors.New("no audio output · d to pick")
	}
	return nil
}

func (m *Mock) Next() error {
	i := m.indexOf(m.session.TrackID)
	if i < 0 || i+1 >= len(m.playlist.Tracks) {
		return errors.New("no next track")
	}
	next := m.playlist.Tracks[i+1]
	return m.Play(next)
}

func (m *Mock) Prev() error {
	i := m.indexOf(m.session.TrackID)
	if i <= 0 {
		return errors.New("no previous track")
	}
	prev := m.playlist.Tracks[i-1]
	return m.Play(prev)
}

func (m *Mock) Enqueue(track library.Track) error {
	m.queue = append(m.queue, track)
	m.session.QueueLen = len(m.queue)
	return nil
}

func (m *Mock) VolumeUp() error { return m.volumeDelta(10) }

func (m *Mock) VolumeDown() error { return m.volumeDelta(-10) }

func (m *Mock) volumeDelta(d int) error {
	if m.session.State == library.StateNoOutput {
		return errors.New("no audio output · d to pick")
	}
	v := m.session.Volume + d
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	m.session.Volume = v
	return nil
}

func (m *Mock) indexOf(id string) int {
	for i := range m.playlist.Tracks {
		if m.playlist.Tracks[i].ID == id {
			return i
		}
	}
	return -1
}

type seed struct {
	title, artist, album, added string
	min, sec                    int
	saved                       bool
}

func (s seed) dur() time.Duration {
	return time.Duration(s.min)*time.Minute + time.Duration(s.sec)*time.Second
}

// trackSeed is the 42-track mock playlist from the chosen artboard.
var trackSeed = []seed{
	{"So What", "Miles Davis", "Kind of Blue", "12 Nov", 9, 22, true},
	{"Moanin'", "Art Blakey", "Moanin'", "11 Nov", 9, 34, true},
	{"Giant Steps", "John Coltrane", "Giant Steps", "10 Nov", 4, 46, false},
	{"'Round Midnight", "Thelonious Monk", "Genius of Modern Music", "9 Nov", 6, 38, true},
	{"Blue in Green", "Miles Davis", "Kind of Blue", "8 Nov", 5, 38, true},
	{"Chega de Saudade", "João Gilberto", "Chega de Saudade", "7 Nov", 2, 2, false},
	{"Bemsha Swing", "Thelonious Monk", "Brilliant Corners", "6 Nov", 7, 41, true},
	{"A Night in Tunisia", "Dizzy Gillespie", "The Champ", "5 Nov", 3, 6, false},
	{"Freddie Freeloader", "Miles Davis", "Kind of Blue", "4 Nov", 9, 47, true},
	{"Misterioso", "Thelonious Monk", "Thelonious in Action", "3 Nov", 9, 21, false},
	{"Naima", "John Coltrane", "Giant Steps", "2 Nov", 4, 21, true},
	{"Desafinado", "Stan Getz", "Getz/Gilberto", "1 Nov", 5, 49, false},
	{"All Blues", "Miles Davis", "Kind of Blue", "31 Oct", 11, 33, true},
	{"Take Five", "Dave Brubeck", "Time Out", "30 Oct", 5, 24, true},
	{"Straight, No Chaser", "Thelonious Monk", "Straight, No Chaser", "29 Oct", 8, 3, false},
	{"Flamenco Sketches", "Miles Davis", "Kind of Blue", "28 Oct", 9, 26, true},
	{"Confirmation", "Charlie Parker", "Now's the Time", "27 Oct", 3, 0, false},
	{"Ornithology", "Charlie Parker", "Bird at the Roost", "26 Oct", 3, 1, false},
	{"Anthropology", "Charlie Parker", "The Complete Savoy", "25 Oct", 3, 12, true},
	{"Donna Lee", "Charlie Parker", "The Complete Savoy", "24 Oct", 2, 34, false},
	{"Ko-Ko", "Charlie Parker", "The Complete Savoy", "23 Oct", 2, 56, false},
	{"Salt Peanuts", "Dizzy Gillespie", "Groovin' High", "22 Oct", 3, 17, true},
	{"Groovin' High", "Dizzy Gillespie", "Groovin' High", "21 Oct", 2, 41, false},
	{"Hot House", "Dizzy Gillespie", "Groovin' High", "20 Oct", 3, 8, false},
	{"Blue Monk", "Thelonious Monk", "Blue Monk", "19 Oct", 7, 37, true},
	{"Epistrophy", "Thelonious Monk", "Blue Monk", "18 Oct", 3, 14, false},
	{"Well, You Needn't", "Thelonious Monk", "Blue Monk", "17 Oct", 3, 22, false},
	{"Ruby, My Dear", "Thelonious Monk", "Blue Monk", "16 Oct", 6, 20, true},
	{"Four on Six", "Wes Montgomery", "The Incredible Jazz Guitar", "15 Oct", 6, 9, false},
	{"West Coast Blues", "Wes Montgomery", "The Incredible Jazz Guitar", "14 Oct", 7, 24, false},
	{"Song for My Father", "Horace Silver", "Song for My Father", "13 Oct", 7, 17, true},
	{"Moanin' (alt take)", "Art Blakey", "Moanin'", "12 Oct", 9, 47, false},
	{"Cool Struttin'", "Sonny Clark", "Cool Struttin'", "11 Oct", 9, 27, true},
	{"Blues Walk", "Lou Donaldson", "Blues Walk", "10 Oct", 6, 23, false},
	{"Watermelon Man", "Herbie Hancock", "Takin' Off", "9 Oct", 7, 9, true},
	{"Cantaloupe Island", "Herbie Hancock", "Empyrean Isles", "8 Oct", 5, 29, false},
	{"Maiden Voyage", "Herbie Hancock", "Maiden Voyage", "7 Oct", 7, 53, true},
	{"Footprints", "Wayne Shorter", "Adam's Apple", "6 Oct", 7, 30, false},
	{"Speak No Evil", "Wayne Shorter", "Speak No Evil", "5 Oct", 8, 22, true},
	{"Yes or No", "Wayne Shorter", "JuJu", "4 Oct", 6, 36, false},
	{"Acknowledgement", "John Coltrane", "A Love Supreme", "3 Oct", 7, 43, true},
	{"Resolution", "John Coltrane", "A Love Supreme", "2 Oct", 7, 20, false},
}
