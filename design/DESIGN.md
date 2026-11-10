---
product: spotify-tui (working name)
colors:
  canvas: theme.canvas # list body, palette body
  rail: theme.rail # left library rail, right queue rail, playing row tint
  surface: theme.surface # selected row, player bar, palette, output picker
  text:
    primary: theme.text # titles, current row, now playing title
    secondary: theme.text-2 # table values, unselected titles, rail items
    muted: theme.muted # labels, numbers, durations, hints, inactive background
  accent: theme.accent # selection, focus, cursor, progress done, "you" markers
  state:
    live: theme.live # playing glyph, active output dot, volume fill
    warning: theme.warn # region lock, degraded session
    error: theme.error # auth failure, dead output
    info: theme.info # sync and rate-limit notices
  border: theme.border # pane rules, box-drawing edges, separators
roles:
  title: bold + text.primary
  current: bold + text.primary on surface
  value: normal + text.secondary
  number: normal + text.muted, right aligned
  label: text.muted, 1 cell inset
  meta: text.muted, after a `·` divider
  live: live glyph + text.primary
glyphs:
  playing: ▶
  active: ●
  idle: ○
  selected: ▸
  collapsed: ▸
  expanded: ▾
  saved: "[x]"
  not-saved: "[ ]"
  not-applicable: ·
  truncation: …
  divider: │
  rule: ─
  progress-done: ━
  progress-knob: ●
spacing:
  inset: 1 col
  rail-inset: 1 col
  group-gap: 1 row
  pane-rule: 1 col or 1 row
widths:
  terminal: 180
  breakpoint: 140
  narrow: 80
  drawer: 8
  palette: 104
  picker: 60
---

# spotify-tui — terminal design

## 1. Overview

A player for a **Spotify Premium** account that runs **locally**: the audio comes out of this machine through an embedded Spotify session, and this app *is* the player. No Spotify app, no phone, no second device.

Users: people who live in a terminal and do not want to switch context to a browser or a phone to put on a track. They already know `lazygit`, `k9s` or `lazydocker`.

Jobs, in order: **search any track and play it**, **browse albums, playlists and liked songs**, **manage the queue and pick the local audio output**. The north-star metaphor is **a file browser for your music library**: one full-screen list, a table, a queue drawer, a status bar, keys you learn once.

Keyboard model: `k9s`/`lazygit` — no modal editing, no `normal`/`insert`, no count prefixes. Single keys act on the focused row, `/` filters the current list, `:` opens a command palette, `?` shows every key. Every action is also reachable from `:`, so nothing is keyboard-only folklore.

**The Local Player Rule.** This app *is* the player: the audio comes out of this machine, through an embedded Spotify session, so no Spotify app and no second device is involved. Controls act locally and immediately; the failure that matters is the output, so `no audio output · d` is a state with its own key, not an error.

**The One Screen Rule.** One list owns the terminal. `tab` and `1`–`4` swap the whole screen to another view; nothing is ever split into side-by-side panes in v1, because a 100-column list loses the album column it needs. `enter` acts on the row under the cursor.

## 2. Colors

Roles only; the concrete values live in the theme file, and the default theme (the one in the artboards) is `canvas #101215 · rail #16191d · surface #1c2026 · border #262b33 · text #e6e9ee · text-2 #a8b0bd · muted #7d8798 · accent #69b7e8 · live #7fd18c`. Users can point every role at their terminal palette; nothing in the app is outside these roles.

- **Canvas** — list bodies and the palette body. The default reading surface.
- **Rail** — the queue drawer band and the tint behind the playing row. A second neutral, slightly lighter than canvas, used as a horizontal band, never as a column.
- **Surface** — the selected row, the player bar, palette chrome, output picker. Use for *state*, never for decoration.
- **Accent** — selection, focus, terminal cursor in the palette input, the completed part of the progress track, and markers that mean "yours" (`you` in playlist metadata).
- **Live** — the playing glyph, the active-output dot, the volume fill. The only green in the screen.
- **Muted** — labels, row numbers, durations, hints, inactive background content. Minimum 4.5:1 against every surface it lands on; if a muted value cannot hold that contrast, demote the information by *placement*, not by making it dimmer.

**The Single Accent Rule.** One accent hue, and it only means action, selection or live state. Never color a row, a header and a border in the same frame; if two things need emphasis, one is wrong.

**The Never-Color-Alone Rule.** Every state carries a glyph or a word: playing is `▶` plus the number replaced, saved is `[x]`, unavailable is `[ ]` plus `region lock`.

## 3. Type roles

Four roles, applied identically across panes. There is no font size hierarchy in a cell grid: hierarchy is role, weight and alignment.

- **title** — bold, primary. Track titles, album names, the palette header, the now-playing title.
- **current** — bold, primary, on surface. The focused row. Exactly one per focused pane.
- **value** — normal, secondary. Table cells, artist and album names, list rows, results.
- **number** — normal, muted, right aligned. Position in the list, duration, counts. Never italic.
- **label** — muted, 1 cell inset. Column headers, section headers (`▾ PLAYLISTS 12`), the status-bar key names.
- **meta** — muted, joined by `·`. Secondary facts on one line: `Miles Davis · Kind of Blue · 1959`.

**The Same-Role Rule.** A role looks the same in every pane. A muted duration in the track table is the same muted duration in the queue; if the queue needs a different treatment, that is a different role, named in the frontmatter.

**The No-Caps Rule.** Section headers are uppercase only when they are structural (`QUEUE`, `OUTPUT`); track, album and artist text keeps its real casing, and body copy is never letter-spaced.

## 4. Layout

At 180×50: one full-screen list, a one-row context strip, a queue drawer of 8 rows, two rows for the player and one for the status bar.

- **Top bar** — brand, then the **two** sections as `│` cells: `│ Tracks │ Playlists`. The active section sits on `surface`, bold; the other stays muted. `Tracks` is whatever list is open (playlist, album, search result) and names it in the context strip; `Playlists` is the browse view. Search, the palette, the output picker and help are **floating windows**, not sections, so they never take a cell. Account on the far right. Never output state.
- **Context strip** — one row: where you are (`playlists / Bebop Essentials`), owner and privacy, then track count, total time and the current sort. The only place those facts appear.
- **List** — owns every column the terminal has, minus 2 cells of inset. A view is one list: library, album, playlist, search results, queue.
- **Queue drawer** — 8 rows above the player, toggled with `q`, tinted with `rail` and closed by one `─` rule. It shows *up next only*: the current track already lives in the player bar.
- **Player bar** — always two rows at ≥140 columns: row 1 = state glyph, title, artist/album/year, audio output and bitrate; row 2 = progress track with times, shuffle/repeat mode, volume. At 80 columns it collapses to one row and drops the times, keeping the progress track.
- **Status bar** — one row, fixed at the bottom, contextual keys on the left in the order you need them, view state and clock on the right, separated by 3-cell gaps.

Breakpoints, keyed on terminal columns:

| Columns | Layout |
| --- | --- |
| ≥ 140 | full list + drawer; every column of the table visible |
| 100–139 | full list + drawer; the table drops `ADDED`, keeping `# · TITLE · ARTIST · ALBUM · SAVED · TIME` |
| 80–99 | full list, drawer becomes a full view (`4`), player collapses to one row |
| < 80 | refuse to render the list, print the minimum size and the view keys |

**The Sections-Only Rule.** The title row carries the places you return to — `Tracks` and `Playlists`, two cells, not a menu. Search is a window you open and close, not a place you are; so are the palette (`:`), the output picker (`d`) and help (`?`). Anything that is transient belongs to a floating window, and adding a section is a product decision that has to earn a permanent cell. The row never grows a second line and extends sideways if a third section is ever justified.

**The One Rule Row Rule.** Regions are separated by a single 1-cell rule — above the drawer, above the player, above the status bar — never by gaps plus frames. Box-drawing is reserved for two things: the palette and the audio output picker, the only floating, independently scrolling regions on screen.

## 5. Surfaces

Three surfaces and nothing else: **canvas** (reading), **rail** (navigation), **surface** (state and floating panels). Elevation is not a metaphor here — the floating palette is a surface *plus* an edge, because it must read as separate from a scrolling list behind it.

The player bar is surface, full width, and separates from the list by one `─` rule. The status bar is canvas with a top border, so the two bars do not merge into one block. The queue drawer is rail: it is chrome, not content, and must read as the quieter band.

**The Contrast-Edge Rule.** Every floating panel draws a closed `┌─┐ │ └─┘` edge in `border`; every inline region uses tint or a single rule. Never both for the same region.

## 6. Glyphs

One geometric family, one meaning each. No emoji, no icon fonts, no ASCII art.

| Glyph | Meaning |
| --- | --- |
| `▶` | now playing (replaces the row number) |
| `●` | active: selected audio output, live player |
| `○` | idle output, unselected |
| `▸` / `▾` | collapsed / expanded section, and the selected rail node |
| `[x]` / `[ ]` | saved to your library / not saved; `[ ]` plus `region lock` when it cannot be saved |
| `·` | divider inside meta lines |
| `│` | segment divider in bars and strips |
| `─` | rule, and the remaining part of the progress track |
| `━` + `●` | progress elapsed, and the playhead |
| `…` | truncation marker; lists truncate, they never wrap |
| `✓` / `✗` | command results in the palette (`:output` success, auth failure) |

**The Measured Glyph Rule.** Every glyph used in chrome is single-width; emoji and CJK never enter the layout, and every row is padded with `width`-aware truncation so nothing overflows its column budget.

## 7. Components

**Library view** — full screen, the same list component: section headers `▾ PLAYLISTS 12`, `▾ ALBUMS 84`, `▾ ARTISTS 61`, and collapsed placeholders (`▸ GENRES 14`) that expand with `l`. Two levels only: collection, then its contents. Rows are one line, name left, count or year right. States: default, selected (surface tint, bold, `▸`), loading (`○ …`), empty (`○ no playlists`), error (`✗ could not load · r retry`).

**Track table** — columns `# · TITLE · ARTIST · ALBUM · SAVED · TIME` on a full screen, `+ ADDED` at ≥140. Sortable with `,`. Row states: default, alternate (no zebra — proximity only), focused (`current` role), playing (`▶` + live glyph on the number, rail tint), unavailable (struck title, `[ ]`, `region lock` in the album cell), loading (skeleton row with muted `…`). Bulk selection uses `v` (k9s style) and marks rows with `▸` in a 1-cell gutter. `l` toggles SAVED and the cell answers immediately.

**Queue drawer** — `▾ QUEUE  up next 8 of 41` on the band header, then eight rows of what plays next with artist and duration; the current track is *not* repeated because the player bar owns it. Reorder with `J`/`K`; removing the current track asks nothing and skips forward. At 80 columns the drawer is not rendered and the queue becomes view `4`.

**Player bar** — described in Layout. States: playing, paused (`‖` glyph, progress frozen, live color leaves the title), no audio output (`○ no output · d to pick` replaces the output segment), buffering (`◐` in the glyph cell, title unaffected), auth expired (`✗ session expired · :login`), device asleep (output segment says `no output`; queue commands still work, they only touch the queue).

**Floating windows** — search (`/`), the command palette (`:`) and the output picker (`d`) share one component: a closed `┌─┐ │ └─┘` edge in `border` on `surface`, centred over the list, opened by its own key and closed with `esc`. One at a time; the section row and the context strip never change while one is open, because a window is not a place. The list behind stays put and does not scroll. The window sizes to its content, never to the screen, and long result sets scroll inside it.

**Search and palette window** — the same panel in two modes: `/` searches, `:` takes commands. It sizes to its content up to the clamp (104 columns, 14 rows), so a command hint stays narrow and a result list widens to the clamp. A fixed width would put a one-line hint inside a 104-cell empty box. Rows: `⋮` header (`command` + `esc close · ? all keys`), a `─` separator, the input line with an accent `>` prompt and an accent block cursor, then type-grouped results (`tracks 3`, `albums 1`, `playlists 2`) with the matched substring bold and metadata right-aligned, then a separator and a key footer with the result count. States: empty query (`recent`, `:help`, `:queue`, `:output`), no results (`○ nothing matches · tab widens the scope`), long lists scroll with `ctrl+j`/`ctrl+k`, prefix commands complete inline (`:pla` → `:play`).

**Audio output picker** — 60 columns, closed edge, list of local sinks: `● Speakers · PipeWire · active`, `○ HDMI / DisplayPort`, `○ JACK`. Blocked reason inline (`○ Bluetooth headset · not connected`). There is no Spotify device list: the player runs here, and picking a sink moves the sound, not the session.

**Global states** — `player starting…` (brief, at launch), `no audio output · d`, `premium required` (explains the limitation and stops offering playback keys), `rate limited · retrying in 3s` (muted, live spinner only while a retry is genuinely in flight), `empty queue` (`○ queue is empty · enter plays from this list`).

## 8. Do's and Don'ts

**The One Fact Rule.** Each fact appears once per screen. Audio output and bitrate live in the player bar only; track count, total time and sort in the context strip only; the now-playing title never repeats in the queue drawer.

**The Mirror Rule.** Progress and volume are read-only mirrors of the reported session. A local keypress shows as pending (dimmed knob or `…` for up to one second), then either lands or reverts with a one-line toast; the UI never advances progress optimistically.

**The Keys-Once Rule.** Keyboard help lives in the status bar and in `?`, never in pane headers, footers or the context strip. Pane headers carry a name and a count, nothing else.

**The One View Rule.** One list owns the screen; other views replace it, they do not share it. The queue is a drawer, never a column. If a fact needs to be visible while you browse, the player bar is the place for it.

Do:

- show the source of what you are playing (`Kind of Blue`, `Bebop Essentials`) in the strip and in the player bar's artist line.
- right-align every number and keep durations at a stable width.
- truncate long titles with `…` and put the full value in the inspector (`i`), never wrap.
- keep one accent, one live color, one border color.
- explain unavailable actions in place: `enter · premium required`, `d · no audio output found`.

Don't:

- don't render album art pixels, half-blocks, or sixel in v1: cover art is a separate decision with per-terminal protocol risk, and it breaks the cell grid.
- don't invent a per-item colored sidebar (`▌` on every row) or a card per fact.
- don't add blank rows to create hierarchy; use role and alignment. One gap between groups, never everywhere.
- don't put fake function-key bars (`F1 Help F2 Quit`) or a neon-on-black retro palette in the chrome.
- don't animate anything decorative; no spinners except for a genuinely in-flight network action, and honour `prefers-reduced-motion` in any mockup or web surface.
- don't show the same status twice (top bar plus player bar, toast plus row).
