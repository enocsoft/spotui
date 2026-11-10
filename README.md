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
| Floating windows | real: `/`, `:`, `d`, `?` |
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

1. Create an app at <https://developer.spotify.com/dashboard>. Copy its **client ID**.
2. Add this exact redirect URI to the app:

   ```
   http://127.0.0.1:8888/login
   ```

   The path `/login` is fixed by the library and `localhost` is not accepted by Spotify — it has to be
   the loopback **IP literal**. If you change the port in the config, change it here too.
3. Write `~/.config/spotify-tui/config.toml`:

   ```toml
   client_id     = "<your client id>"
   callback_port = 8888      # optional, this is the default
   output        = ""        # optional: preferred sink name, empty = system default
   ```
4. Run it:

   ```sh
   go run ./cmd/spotify-tui
   ```

   The first run shows a full-screen **login required** state with the authorization URL. Open it,
   approve, and the app picks up the callback on `127.0.0.1:8888/login` and starts the player.
   Credentials are cached, so this happens once.

## Playing something today

Until the real catalog lands, the list is fixture data. To hear audio, ask for a track by id:

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
| `?` | help |

One fact appears once per screen: the audio output and bitrate live in the player bar, the collection
count and sort in the context strip, and the queue drawer never repeats the current track.

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
