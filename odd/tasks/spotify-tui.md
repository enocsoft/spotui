# spotify-tui — terminal player for Spotify Connect

Player de terminal que controla una cuenta Spotify **Premium** de forma **remota** (Spotify Connect): el audio suena en el dispositivo activo de la cuenta y la app es el control. Sin motor de audio local en v1. Estética y modelo de teclado `k9s`/`lazygit`, sin modos.
Diseño de referencia: `design/DESIGN.md` + `design/spotify-tui/` (3 boards, `01-fullscreen-list.html` es la dirección elegida).

## Specs

S1. Control remoto, no playback local: toda acción de reproducción es un pedido a la API y la UI refleja el estado **reportado** por Spotify; `no active device` es un estado de primera clase, no un error. (L2)
S2. Jobs, en orden: buscar una canción y reproducirla, navegar álbumes/playlists/biblioteca, manejar la cola y elegir dispositivo. Textual del usuario: «todo buscar cancion reproducir, albunes playlist». (L2)
S3. Modelo de teclado `k9s`/`lazygit`: sin `normal`/`insert`, sin count-prefix, `:` abre el palette de comandos, `?` lista todo, `/` filtra la lista actual. Textual: «Minimalista tipo k9s/lazygit». (L2)
S4. Una sola lista ocupa la pantalla; las vistas la reemplazan, nunca hay paneles lado a lado. La cola es un drawer de 8 filas al pie, toggled con `q`, y no repite el tema actual. Textual: «me gustaría que cada columna fuera full screen / no tres columnas». (L3)
S5. Fila de secciones arriba, con celdas separadas por `│`, sólo las principales: `Library · Search · Queue`. Lo que se resuelve con una tecla (`d` devices, `:` palette, `?` ayuda) no gasta celda; los drill-downs (playlist, álbum, artista) se nombran en la franja de contexto y `esc` vuelve. Si no entran, la fila se corre hacia el costado, nunca a una segunda línea. Textual: «| title | title | title / aca screen» y «poner menos pestañas ... sólo dejar las principales». (L3)
S6. Guardado de tema como casilla: `[x]` guardado, `[ ]` no guardado (más `region lock` cuando no se puede), toggleada con `l` en el lugar, sin diálogo. Textual: «los corazones reemplazalos por [x] 0 []». (L3)
S7. El palette de comandos/búsqueda se conserva tal como está diseñado: borde cerrado, resultados agrupados por tipo, conteo por grupo, metadatos a la derecha. Textual: «esto me gusta que el buscador sea asi y el coso de command». (L3)
S8. Stack: Go con `bubbletea` + `lipgloss`. Sin CGO ni librespot. Textual: «si ese stack me gusta esta perfecto». (L4)
S9. Config y credenciales al estilo `ncspot`: el `client_id` vive en un archivo de config XDG (`~/.config/spotify-tui/config.toml`), el flujo es OAuth **PKCE** ejecutado en la terminal (abrir URL en el navegador cuando hay uno, si no imprimirla), y el refresh token se cachea local con permisos `0600` en `$XDG_STATE_HOME/spotify-tui/token.json`. Nada de servicios ni servidores externos. Textual: «la idea es que haga un o2auth o con el clientID en un archivo total eso va a ser a niver local en terminal todo para que la app funcione tipo un ncspot». (L4)
S10. Slice 1 es el shell de la TUI con datos mock y keymap real; la API real entra en el slice siguiente. Textual: «Arranco por el shell de la TUI». (L5)
S11. Tokens, roles, glifos y reglas con nombre salen de `design/DESIGN.md` y no se inventan valores fuera de ese archivo. (L1-L4)

## Tasks

| ID | Qué | Specs | Ruta | Commit |
| --- | --- | --- | --- | --- |
| T1 | `go.mod` (`github.com/nahuel/spotify-tui`), `cmd/spotify-tui/main.go` con `tea.NewProgram`, carga de config XDG (`client_id`, defaults) y error legible si falta | S8, S9, S10 | inline | slice 1 |
| T2 | Theme/tokens desde `design/DESIGN.md` en un paquete `internal/ui/theme` (roles, sin hex sueltos en las vistas) | S11 | inline | slice 1 |
| T3 | Layout shell: fila de secciones, franja de contexto, lista full-screen, drawer de cola, player bar de 2 filas, status bar de 1 fila | S1, S4, S5 | inline | slice 1 |
| T4 | Keymap k9s: `j/k/g/G/ctrl+d/ctrl+u`, `enter`, `a`, `space`, `n/p`, `tab`, `q`, `l`, `esc`, `H/L`, `/`, `:`, `d`, `?` | S3, S5, S6 | inline | slice 1 |
| T5 | Datos mock: biblioteca (secciones + filas), playlist con 42 tracks, sesión Connect, cola de 41 | S1, S2 | inline | slice 1 |
| T6 | Tests: render a 180x50 / 140 / 80 sin desbordes, fila de secciones no crece a 2 líneas, `[x]` toggle con `l`, `q` abre/cierra drawer | S4, S5, S6 | inline | slice 1 |
| T7 | *Slice 2*: cliente Web API + OAuth PKCE + cache de token + `GET/PUT /me/player` | S1, S2, S9 | pendiente | — |

## Log

L1 (pedido original, verbatim): «hola quiero dise;ar un music player que funcione con spotify pero en la terminal tipo nvim pero vamos a hacer el dise;o primero»

L2 (decisiones de producto, verbatim de las respuestas):
- Reproducción: «Control remoto (Spotify Connect)»
- Trabajo principal: «todo buscar cancion reproducir, albunes playlist»
- Idioma vim: «Minimalista tipo k9s/lazygit»

L3 (comentarios de diseño, verbatim):
- «me gustaría que cada columna fuera full screen / no tres columnas»
- «los corazones reemplazalos por [x] 0 []»
- «esto me gusta que el buscador sea asi y el coso de command»
- «algo asi quiero | title | title | title / aca screen / y si quiere otra title tien que ir haci el costado»
- «claro asi muy bien pero hay que ver de poner menos pesta;as yya que algunas pueden ser por atajos solo dejar las principales»

L4 (stack y credenciales, verbatim): «si ese stack me gusta esta perfecto» → Go + bubbletea. Y «la idea es que haga un o2auth o con el clientID en un archivo total eso va a ser a niver local en terminal todo para que la app funcione tipo un ncspot me entendes?» → client_id en config.toml, PKCE en terminal, token cacheado local.

L5 (arranque, verbatim): «Arranco por el shell de la TUI» → slice 1 es el shell con datos mock.

L6 (supuesto explícito, corregir si no corresponde): «tipo ncspot» se toma como **modelo de config y auth**, no como playback local. ncspot suena en la máquina con librespot; acá seguimos con Connect remoto porque así se eligió en L2 y porque librespot es Rust, lo que rompería S8.
