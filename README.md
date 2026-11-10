# spotify-tui

A terminal player for Spotify that **plays locally**: the audio comes out of this machine through an
embedded Spotify session. No Spotify app, no phone, no Connect device to transfer.

Keyboard model borrowed from `k9s` and `lazygit`: no modal editing, single keys, `:` for commands,
`/` for search, `?` for every key. The design system and the three mockup directions live under
[`design/`](design/DESIGN.md); the feature spec and decisions live in
[`odd/tasks/spotify-tui.md`](odd/tasks/spotify-tui.md).

## Status

| Area | State |
| --- | --- |
| Playback engine | real: embedded Spotify session, PulseAudio sinks, `space`/`n`/`p`/`+`/`-` |
| Login | real: full-screen state with the auth URL, credentials cached in the XDG state dir (0600) |
| Interface | real: section row, context strip, full-screen list, queue drawer, player bar, status bar |
| Floating windows | real: `/` (input only), `:`, `d`, `?` |
| Library | real: your playlists, saved albums, playlist and album contents, search |
| Radio | real: `R` keeps playing related tracks after the current one |
| Login | real: `o` opens the authorization page in your default browser |
| Library and search data | **mock** — the rows are fixed fixture data, not your account (next slice) |
| Playing a track | `:play spotify:track:<id>` works now; pressing `enter` on a mock row does not |

### This is an unofficial client

It speaks the Spotify Connect protocol through [`go-librespot`](https://github.com/elxgy/go-librespot),
the same approach as `ncspot` and `spotifyd`. It is **not** an official Spotify integration, it is for
**personal, non-commercial use**, and it **requires Spotify Premium** — librespot does not support free
accounts, and Spotify refuses to play without an active subscription.

## Requirements

- Linux with PulseAudio or PipeWire (`pipewire-pulse` works).
- A C toolchain, because the FLAC and audio-output paths use CGO: `gcc`, `pkg-config`, and the
  development headers for `alsa` and `libpulse`.
- Spotify **Premium**.

`CGO_ENABLED=0` cannot build this project: `go-librespot/flac` has no pure-Go fallback. Plain
`go build` (CGO on by default) is the intended path.

## Setup

Playback needs **no dashboard app**: the login is Authorization Code + PKCE done by the app with
librespot's own client id, the same identity `ncspot` and `spotifyd` use.

1. Run it:

   ```sh
   go run ./cmd/spotify-tui
   ```

2. The first run shows a **login required** screen with the authorization URL, wrapped over several
   lines, and saves the same URL verbatim to `$XDG_STATE_HOME/spotify-tui/login-url.txt` (usually
   `~/.local/state/spotify-tui/login-url.txt`). Open it, approve, and the app picks up the callback on
   `127.0.0.1:8888/login`. Credentials are cached in the state dir with `0600` permissions, so this
   happens once.

   Keep the app running while you approve, and do not reload the callback page afterwards: the
   callback is single-use.

3. Optional, only for the library and search slice: `client_id` in
   `~/.config/spotify-tui/config.toml` names a Spotify dashboard app used for Web API calls, and that
   app needs `http://127.0.0.1:8888/login` registered as a redirect URI (loopback **IP literal**;
   `localhost` is rejected by Spotify). Playback does not use it.

   ```toml
   client_id     = "<dashboard app client id>"
   callback_port = 8888      # optional, this is the default
   output        = ""        # optional: preferred sink name, empty = system default
   ```

## Playing something today

The library is yours once you log in. `:play` also takes a playlist or album, pasted as a link:

```
:play spotify:track:4uLU6hMCjMI75M1A2tKUQC
```

A bare 22-character base62 id works too. Anything else gets a toast telling you what a track id looks
like. Pick the output with `d`; the current track resumes on the new sink.

## Keys

| Key | Action |
| --- | --- |
| `j` `k` / arrows | move the cursor |
| `g` `G` / `ctrl+d` `ctrl+u` | top, bottom, half page |
| `enter` | play the row under the cursor |
| `a` | enqueue |
| `space` | play / pause |
| `n` `p` | next / previous track |
| `+` `-` | volume |
| `tab` | next section (`Tracks`, `Playlists`) |
| `q` | toggle the queue drawer |
| `l` | toggle the row in your library |
| `esc` | close a window, leave a drill-down |
| `/` | search window |
| `:` | command window |
| `d` | audio output window |
| `s` | shuffle the open list (our order then drives `n`/`p`) |
| `R` | radio: keep playing related tracks after this one |
| `o` | on the login screen, open the authorization page in your browser |
| `?` | help |

One fact appears once per screen: the audio output and bitrate live in the player bar, the collection
count and sort in the context strip, and the queue drawer never repeats the current track.

## Troubleshooting

Every item below cost a real debugging session, in this order.

| Symptom | Cause and fix |
| --- | --- |
| Spotify says `response_type must be code` | A binary built before the PKCE login. The login screen prints `build <revision>` at the bottom: if it is missing or old, rebuild and run again. |
| The browser says "login complete" and the terminal never moves | The session failed and the error was hidden behind the login screen. Update: failures outrank the login state since `dc5f9f8`. |
| `failed exchanging oauth2 code: "code must be supplied"` | Something touched `127.0.0.1:<port>/login` before the real redirect (a restored tab, a reload, a favicon request). The callback is single-use: close old tabs, open the authorization URL in a fresh one and do not reload the callback page. |
| `unknown credentials: *session.SpotifyTokenCredentials` | A credentials value passed by pointer. Fixed; the library type-switches on the concrete type. |
| `accesspoint login failed: BadCredentials` | A Web API token where the access point wants a session token. Playback must log in with librespot's own client id. |
| Nothing plays after `:play spotify:track:<id>` | Check the audio output with `d`, and confirm the id is 22 base62 characters. Full `open.spotify.com` links are not accepted yet. |
| The list shows "Bebop Essentials" and tracks you never saved | That is fixture data. The real library and search are the pending Web API slice. |
| `CGO_ENABLED=0 go build` fails | Expected: the FLAC decoder has no pure-Go fallback. Build with CGO on. |
| The build revision has `+dirty` | The binary was built from a worktree with uncommitted changes. Fine for development, useless as a bug report. |

The player log lives in `$XDG_STATE_HOME/spotify-tui/player.log` and redacts token-shaped runs, so it is
safe to paste. It is the first place to look when audio does not start.

## Development

```sh
gofmt -l cmd internal
go vet ./...
go test -count=1 ./...
CGO_ENABLED=1 go build ./...     # CGO is required
SPOTIFY_TUI_MOCK=1 go run ./cmd/spotify-tui   # offline shell, no credentials needed
```

The test suite needs no Spotify account and no network: it covers layout widths at 180, 140 and 80
columns, the window geometry, the keymap, config parsing and the pure player mappings.
