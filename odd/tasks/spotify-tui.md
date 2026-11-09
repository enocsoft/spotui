# spotify-tui — terminal player for Spotify Connect

Player de terminal que controla una cuenta Spotify **Premium** de forma **remota** (Spotify Connect): el audio suena en el dispositivo activo de la cuenta y la app es el control. Sin motor de audio local en v1. Estética y modelo de teclado `k9s`/`lazygit`, sin modos.
Diseño de referencia: `design/DESIGN.md` + `design/spotify-tui/` (3 boards, `01-fullscreen-list.html` es la dirección elegida).

## Specs

S1. Playback **local**: la app *es* el reproductor y el audio sale de la máquina donde corre; no hace falta abrir Spotify ni tener otro dispositivo. El protocolo Connect se implementa con `go-librespot` embebido (S15), no con los endpoints de Player de la Web API. Textual: «nono yo quiero que el sonido salga desde la terminal no tener que abierto spotify». (L8)
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

S12. Redirect URI del OAuth: `http://127.0.0.1:PORT/callback` con **IP literal** (Spotify permite HTTP sólo en loopback); `localhost` está prohibido como redirect y el puerto puede ser dinámico si se registra sin puerto. (L7)
S13. Development Mode alcanza y es el modo del proyecto: 1 Client ID por developer, hasta 5 usuarios autorizados, el dueño necesita Premium activo. Proyecto personal, no comercial. (L7)
S14. Restricciones de la Web API (Feb-2026) que limitan features: contenidos de playlist sólo si sos owner o collaborator (fuera de eso, metadata y un estado que explique por qué no hay temas); `GET /search` con `limit` máximo 10 (paginar con `offset`); sin `GET /tracks|albums|artists` batch, sin `browse/*`, sin `artists/{id}/top-tracks`, sin datos de otros usuarios; sin `popularity`/`followers`/`label`/`available_markets`; campos de playlist renombrados `tracks` → `items`. (L7)

S15. Implementación del playback: **`github.com/devgianlu/go-librespot`** embebido en el binario (su `docs/embedding.md` está escrito para este caso, incluye flujo de auth único para app + sesión librespot). Backends de audio ALSA/PulseAudio. Requiere Spotify Premium. CGO probable: verificar antes de comprometerse y, si CGO es inevitable, dejarlo declarado en el README. (L8)
S16. Control local, no remoto: `space`/`n`/`p`/volumen hablan con el player en proceso; no hay transferencia a dispositivo ni `NO_ACTIVE_DEVICE`. La salida de audio es un **sink local** y `d` abre el selector de salidas, no una lista de dispositivos Spotify. (L8)
S17. Alcance no oficial, declarado: un cliente librespot no es una integración oficial de Spotify y se mueve en la misma zona gris que ncspot/spotifyd (que el usuario tomó como referencia). El README lo dice explícitamente y el proyecto es de uso personal, no comercial. (L8)

## Tasks

| ID | Qué | Specs | Ruta | Commit |
| --- | --- | --- | --- | --- |
| T1 | `go.mod` (`github.com/nahuel/spotify-tui`), `cmd/spotify-tui/main.go` con `tea.NewProgram`, carga de config XDG (`client_id`, defaults) y error legible si falta | S8, S9, S10 | inline | slice 1 |
| T2 | Theme/tokens desde `design/DESIGN.md` en un paquete `internal/ui/theme` (roles, sin hex sueltos en las vistas) | S11 | inline | slice 1 |
| T3 | Layout shell: fila de secciones, franja de contexto, lista full-screen, drawer de cola, player bar de 2 filas, status bar de 1 fila | S1, S4, S5 | inline | slice 1 |
| T4 | Keymap k9s: `j/k/g/G/ctrl+d/ctrl+u`, `enter`, `a`, `space`, `n/p`, `tab`, `q`, `l`, `esc`, `H/L`, `/`, `:`, `d`, `?` | S3, S5, S6 | inline | slice 1 |
| T5 | Datos mock: biblioteca (secciones + filas), playlist con 42 tracks, sesión Connect, cola de 41 | S1, S2 | inline | slice 1 |
| T6 | Tests: render a 180x50 / 140 / 80 sin desbordes, fila de secciones no crece a 2 líneas, `[x]` toggle con `l`, `q` abre/cierra drawer | S4, S5, S6 | inline | slice 1 |
| T7 | *Slice 2*: cliente Web API (browse/search/library) + OAuth PKCE con redirect `http://127.0.0.1:PORT/callback` + cache de token | S2, S9, S12, S14 | pendiente | — |
| T9 | Integrar `go-librespot` embebido: sesión, auth único, login y arranque del player desde `cmd/spotify-tui`; medir CGO y backends disponibles en esta máquina | S1, S15, S17 | pendiente | — |
| T10 | Reemplazar el picker de dispositivos por selector de salida de audio local y los estados del player por estados del reproductor propio | S16 | pendiente (abre T4/T5) | — |
| T8 | Estados de restricción en la UI: playlist sin items (no sos owner), búsqueda paginada de a 10, campos ausentes (`popularity`, `followers`) leídos como opcionales | S14 | pendiente | — |

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

L7 (restricciones externas verificadas, 2026): documentación oficial consultada — `concepts/quota-modes`, `blog/2026-02-06-update-on-developer-access-and-platform-security`, `tutorials/february-2026-migration-guide`, `concepts/redirect_uri`, `reference/start-a-users-playback`. Consecuencias: (a) los endpoints de Player siguen disponibles y exigen Premium, así que el plan de control remoto se sostiene; (b) el flujo PKCE local funciona con `http://127.0.0.1:PORT/callback` y NO con `localhost`; (c) `PUT /me/player/play` devuelve `NO_ACTIVE_DEVICE` si no hay dispositivo Connect disponible, por lo que hay que abrir Spotify en algún equipo antes de reproducir; (d) el audio nunca sale de la terminal: sale del dispositivo elegido; (e) search queda limitado a 10 resultados por request y las playlists ajenas no exponen sus temas.

L8 (cambio de requerimiento, verbatim): «nono yo quiero que el sonido salga desde la terminal no tener que abierto spotify». Reemplaza L2: deja de ser control remoto por Connect y pasa a ser reproductor local. Consecuencias registradas: S1 y S2 reescritos, S15–S17 agregados, T4/T5 reabiertos, T7 acotado a browse/auth y nuevos T9 (go-librespot embebido) y T10 (selector de salida). El audio ya no depende de `PUT /me/player/play`: los endpoints de Player de la Web API dejan de usarse para reproducir. Sigue requiriendo Premium (limitación de librespot) y sigue siendo un cliente no oficial.

L9 (fronteras de slice, para el revisor): `1e71fb6` = diseño (DESIGN.md + 3 boards + manifest, casi todo HTML de mockups); el commit de docs siguiente = restricciones de API 2026 (S12–S14) + pivote a playback local (S1 reescrito, S15–S17) + wording de DESIGN.md; el slice 1 de código (T1–T6, más T10 si entra) va en su propio commit con tests al lado, después de verificar build/vet/tests.

L10 (nota de proceso): dos commits de docs anteriores se hicieron con `git add -A` mientras el worker escribía y barrieron `go.mod`, `go.sum` e `internal/**` dentro de un commit de documentación. Se rehizo la historia local (nada pusheado) y desde ahora los commits de docs usan rutas explícitas. El código del worker se commitea recién cuando termina y pasa build/vet/tests.
