# starlight 🌟

Agente de IA de línea de comandos escrito en **Go puro** (sólo biblioteca
estándar, **cero dependencias externas**) que se conecta a cualquier API
compatible con OpenAI y puede leer archivos y ejecutar comandos en la máquina
donde corre.

Pensado para funcionar en equipos **i386 (linux/386)**: el binario es estático,
no usa `cgo`, no arrastra librerías C y aplica límites explícitos de memoria y
tiempo para no desbordar una máquina de 32 bits.

```
$ starlight
🤖 Starlight listo. Escribe tu instrucción ('/ayuda' para ayuda, 'salir' para terminar).
   modelo=gpt-4o-mini  endpoint=https://api.openai.com/v1  max-loops=5

> revisa el espacio libre en disco y dime si hay algo raro
  [🧠 Pensando...]
  [⚙️  Ejecutando herramienta: ejecutar_comando]
  [🧠 Pensando...]
/ está al 62% (18G libres) y /var/log pesa 1.2G; nada anómalo.
```

## Instalación

### Compilar para i386 desde una máquina moderna (cross-compiling)

Con Go instalado, no hace falta compilar en la máquina de 32 bits:

```bash
GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o starlight-linux-386 .
# o, con el Makefile:
make 386          # compila y verifica que el ELF sea de 32 bits
make all          # 386 + amd64 + arm64
```

El resultado es un binario estático de unos **6.6 MB** (ELFCLASS32) que se copia
a la máquina destino por `scp`, `ftp` o USB:

```bash
chmod +x starlight-linux-386
./starlight-linux-386
```

### Compilar en la propia máquina i386

```bash
cd starlight && make 386     # requiere Go 1.23 o superior
```

## Configuración

| Variable | Obligatoria | Por defecto | Descripción |
|---|---|---|---|
| `OPENAI_API_KEY` | sí | — | Clave de la API. |
| `OPENAI_BASE_URL` | no | `https://api.openai.com/v1` | Sirve para OpenRouter, LocalAI, vLLM, Ollama, LiteLLM… |
| `OPENAI_MODEL` | no | `gpt-4o-mini` | Modelo a usar. |
| `NO_COLOR` | no | — | Si está definida, desactiva los colores ANSI. |

```bash
export OPENAI_API_KEY="tu_clave"
export OPENAI_BASE_URL="https://api.openai.com/v1"
export OPENAI_MODEL="gpt-4o-mini"
./starlight-linux-386
```

También se puede pasar todo por banderas: `-modelo`, `-url`, `-max-loops`,
`-timeout`, `-sin-color`, `-version`.

## Uso

```bash
starlight                                   # REPL interactivo
starlight -p "lista los archivos .go del directorio actual"    # una instrucción y sale
starlight "resume el README y dime si falta algo"              # equivalente
```

Comandos del REPL: `/ayuda`, `/limpiar` (olvida la conversación) y `salir`
(también `Ctrl+D`).

Las trazas (`[🧠 Pensando...]`, `[⚙️  Ejecutando herramienta: ...]`) se escriben
en **stderr** y la respuesta final en **stdout**, así que se puede canalizar:

```bash
starlight -p "dime la versión del kernel" > resumen.txt
```

## Herramientas del agente

| Herramienta | Argumentos | Qué hace |
|---|---|---|
| `leer_archivo` | `ruta` | Devuelve el contenido de un archivo de texto. Rechaza directorios y archivos de más de **1 MiB**. |
| `ejecutar_comando` | `cmd`, `timeout_segundos` (opcional) | Ejecuta el comando vía `sh -c` (admite pipes y redirecciones) y devuelve stdout y stderr combinados, recortados a **64 KiB**. Por defecto 120 s de límite. |

El bucle de agente permite hasta **5 iteraciones** de llamadas a herramientas por
turno (`-max-loops`); al agotarlas pide la respuesta final con
`tool_choice: "none"`, de modo que nunca se cuelga en un bucle infinito.

## Decisiones de diseño (y por qué)

- **Cero dependencias.** Garantiza que la compilación cruzada a 32 bits funcione
  sin peleas con `cgo` ni librerías C ausentes.
- **`sh -c` para comandos.** Permite pipes, redirecciones y comillas tal como lo
  haría una persona en la terminal.
- **Límite de 1 MiB en lectura.** Evita que el agente intente cargar un vídeo o
  un log gigante y agote la RAM, que en un i386 es escasa.
- **Muerte del grupo de procesos al expirar el plazo.** `exec.CommandContext`
  por sí solo mata a `sh`, pero sus hijos (`sleep`, scripts, pipes) siguen vivos
  con el pipe abierto y `Wait` se queda esperando: mediado, un `sleep 30` con
  límite de 1 s tardaba **5 s** en volver. Con `Setpgid` + `SIGKILL` al grupo,
  corta en el plazo exacto. Está cubierto por `TestEjecutarComandoTimeout`.
- **Normalización de `arguments`.** La especificación de OpenAI envía los
  argumentos de una herramienta como **cadena** con JSON dentro; varios gateways
  "compatibles" envían un objeto. `decodeArgs` acepta ambos (y vacío/`null`), en
  lugar de fallar con `cannot unmarshal string`.
- **Historial acotado a 41 mensajes** conservando el prompt de sistema y sin
  dejar resultados de herramienta huérfanos (algunos proveedores rechazan un
  mensaje `tool` sin su `assistant` previo).

## Verificación

```bash
make check        # gofmt + go vet + go test  (lo mismo que corre la CI)
./scripts/e2e-i386.sh    # prueba de extremo a extremo en un contenedor i386 real
```

- **17 casos de prueba** (`go test -race ./...`) sobre las herramientas, el
  cliente HTTP, el bucle de agente, el recorte de historial y la configuración.
- La CI (`.github/workflows/ci.yml`) corre `gofmt`, `vet` y `test -race`, compila
  `linux/386`, `linux/amd64` y `linux/arm64`, **verifica que el i386 sea
  ELFCLASS32** (leyendo la cabecera ELF y con `file`), ejecuta el binario dentro
  de un contenedor `i386/debian` y, al publicar un tag `v*`, sube los binarios a
  una release.

### Prueba de extremo a extremo

`scripts/e2e-i386.sh` compila `tools/mockapi` y `starlight` para 386, y dentro de
un contenedor `--platform linux/386` levanta el mock, deja que el agente ejecute
herramientas reales y comprueba la marca de la respuesta final:

```
==> Ejecutando dentro de i386/debian:bookworm-slim (--platform linux/386)
  [🧠 Pensando...]
  [⚙️  Ejecutando herramienta: ejecutar_comando]
  [⚙️  Ejecutando herramienta: leer_archivo]
  [🧠 Pensando...]
arquitectura: i386
RESULTADO-E2E ejecutar_comando=x86_64 32 | leer_archivo=PRETTY_NAME="Debian GNU/Linux 12 (bookworm)" ...

✅ E2E i386 OK: el binario de 32 bits ejecutó herramientas reales y cerró el bucle de agente.
```

`tools/mockapi` implementa el guion de un modelo que pide dos herramientas y
luego resume los resultados; sirve para probar el agente sin gastar tokens ni
necesitar una clave.

## Estructura

```
main.go                      agente completo (configuración, API, herramientas, REPL)
proceso_unix.go              grupo de procesos: matar al hijo y a sus descendientes
proceso_otro.go              alternativa para sistemas sin grupos POSIX
main_test.go                 17 casos de prueba
tools/mockapi/main.go        servidor compatible con OpenAI para pruebas
scripts/e2e-i386.sh          prueba de extremo a extremo en 32 bits
Makefile                     build, test, cross-compiling
```

## Añadir una herramienta

1. Define sus argumentos (`type miArgs struct { ... }`).
2. Escribe su función `herramientaMi...` que devuelva `string`.
3. Añade el `case` correspondiente en `(*agente).ejecutarHerramienta`.
4. Descríbela en `definicionHerramientas()` para que el modelo sepa que existe.

## Licencia

MIT — ver [LICENSE](LICENSE).
