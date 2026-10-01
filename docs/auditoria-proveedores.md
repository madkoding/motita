# Auditoría: conexión a proveedores de LLM (OAuth y API key)

Fecha: 2026-10-01 · Alcance: `internal/llm`, `internal/oauth`, `internal/onboard`,
`internal/config`, el cambio de proveedor en `internal/tui` y `internal/gateway`.

Proveedores exigidos: **OpenAI, Ollama, Claude, Gemini, Qwen, Codex y GitHub Copilot**.

## Resumen

Antes de esta auditoría **ningún login OAuth funcionaba de punta a punta**, y varios
caminos por API key tampoco. Qwen y Ollama local no existían. Todo lo encontrado está
corregido y cubierto por tests en esta rama.

| Proveedor | API key: antes → ahora | Login con cuenta: antes → ahora |
|---|---|---|
| OpenAI | ⚠️ los modelos de razonamiento (o-series, gpt-5) rechazaban `max_tokens`/`temperature` → ✅ | n/a |
| Codex | ❌ los modelos `*-codex` se enviaban a `/chat/completions` (404) → ✅ `/responses` | ❌ no existía → ✅ "Sign in with ChatGPT" (PKCE) |
| GitHub Copilot | ❌ el token de GitHub se enviaba tal cual, sin canje ni cabeceras → ✅ | ❌ el login se abortaba con `slow_down` y el token nunca servía → ✅ |
| Ollama | ✅ Cloud · ❌ local exigía clave → ✅ local sin clave | n/a |
| Claude (Anthropic) | ❌ las herramientas se declaraban mal (400) → ✅ | ❌ OAuth suplantando a Claude Code: incumple los términos y además no funcionaba → retirado; la suscripción va por `claude-code` (CLI oficial) ✅ |
| Gemini | ❌ herramientas mal declaradas; la clave viajaba en la URL → ✅ | ❌ device flow no admitido por Google con el secreto embebido de Gemini CLI → ✅ login de Google con cliente propio o ADC de gcloud |
| Qwen | ❌ no existía → ✅ DashScope | ❌ no existía → ✅ device code de qwen.ai |

## Hallazgos

### Críticos (el proveedor no funcionaba)

1. **Copilot: el token de GitHub nunca se canjeaba.** `llm.go` decía que el canje estaba
   en `internal/llm/copilot.go`, pero ese archivo no existía. El token `gho_…` se enviaba
   como Bearer a `api.githubcopilot.com` sin `Editor-Version` ni `Copilot-Integration-Id`,
   y la API lo rechaza. **Corrección:** `llm/auth.go` canjea el token, lo renueva antes de
   que expire (~30 min), usa el endpoint del plan (individual/business/enterprise) y añade
   las cabeceras.
2. **Copilot: `slow_down` abortaba el login.** `CopilotFullFlow` subía el intervalo y luego
   caía en `return err`. El test existente afirmaba ese comportamiento defectuoso.
   **Corrección:** `oauth.PollDevice` sigue sondeando más despacio (RFC 8628 §3.5).
3. **PKCE mal calculado.** El *challenge* era el hash de los bytes aleatorios y no del
   *verifier* codificado que se envía (RFC 7636 §4.2), así que ningún servidor podía
   validar el `code_verifier`: todo intercambio de código fallaba.
4. **Claude OAuth usaba la cabecera equivocada.** El token OAuth se mandaba como
   `x-api-key`, que solo acepta API keys.
5. **Gemini OAuth:** el *device flow* de Google no concede los scopes de la Gemini API, el
   token se mandaba como `?key=` (que solo acepta API keys) y no se guardaba el refresh
   token. Imposible que funcionara.
6. **Los tokens OAuth caducaban en horas.** El wizard escribía el access token en un `.env`
   que el usuario carga con `source`, sin refresh token: Gemini moría en 1 h, Claude en ~8 h.
   **Corrección:** almacén `~/.motita/auth/<proveedor>.json` (0600, directorio 0700,
   escritura atómica) con renovación automática, y un reintento tras renovar si el
   servidor responde 401.
7. **Codex con API key:** los modelos `gpt-5-codex`/`gpt-5.x-codex` solo existen en
   `/responses`. **Corrección:** cliente de la Responses API (`llm/responses.go`) para el
   proveedor `codex`, con API key o con login de ChatGPT (`chatgpt.com/backend-api/codex`).
8. **Herramientas en Anthropic y Gemini:** `Parameters` ya es un JSON Schema completo y se
   envolvía otra vez como `properties`, declarando tres parámetros llamados `type`,
   `properties` y `required`. Ambas APIs lo rechazan (400), así que el modo con
   herramientas era inutilizable en los dos. Además:
   - Anthropic: el resultado de una herramienta iba precedido de un bloque de texto con el
     mismo contenido (la API exige `tool_result` primero), los resultados paralelos iban en
     turnos separados y los argumentos "stringificados" no se convertían en objeto.
   - Gemini: `functionResponse` llevaba el id de la llamada en vez del **nombre** de la
     función y un string en vez de un objeto; las llamadas no tenían id, por lo que el
     resultado se enviaba como texto suelto; faltaba la `thoughtSignature` que exige
     Gemini 3; una herramienta sin parámetros se declaraba con `properties: {}`, que Gemini
     rechaza.
9. **Cambio de proveedor en la UI:** `SetLLM` cambiaba el proveedor pero conservaba la URL
   base y la clave del anterior: pasar de OpenAI a Anthropic mandaba la siguiente petición
   a `api.openai.com` con la clave de OpenAI. **Corrección:** cada proveedor recupera su
   propia URL y clave (las originales si era el configurado; si no, su URL por defecto y su
   variable de entorno). OpenAI y Codex comparten clave.

### Seguridad y cumplimiento

10. **Clave de Gemini en la URL** (`?key=`): `net/http` imprime la URL en cada error de
    transporte, así que la clave terminaba en los logs. Ahora viaja en `x-goog-api-key`.
11. **Secreto OAuth de Gemini CLI embebido y ofuscado** a propósito para esquivar los
    escáneres de secretos. Retirado: el login usa un cliente OAuth del usuario
    (`MOTITA_GEMINI_CLIENT_ID`/`_SECRET`) o las credenciales ADC de `gcloud`.
12. **OAuth de Claude con el `client_id` de Claude Code:** Anthropic solo permite las
    suscripciones Pro/Max dentro de su propio cliente. Retirado; la vía legítima ya existía
    (`claude-code`, que ejecuta el CLI oficial y nunca ve las credenciales).
13. **`OPENAI_API_KEY` se filtraba a otros proveedores:** se usaba como clave de Anthropic,
    Gemini u Ollama si no había otra, enviando la clave de un proveedor a otro. Ahora solo
    aplica a `openai` y `codex`; cada proveedor tiene su variable (`ANTHROPIC_API_KEY`,
    `GEMINI_API_KEY`, `DASHSCOPE_API_KEY`, `OLLAMA_API_KEY`, `GITHUB_COPILOT_TOKEN`).
14. El callback local de OAuth escucha solo en `127.0.0.1`, valida el `state` y acepta pegar
    la URL a mano (SSH/headless). Tras un login por navegador, el wizard consume el Enter
    pendiente para que el lector de stdin no se trague la siguiente respuesta.

### Menores

15. IDs de modelo inválidos en el catálogo: Copilot `claude-sonnet-4-20250514` (Copilot usa
    `claude-sonnet-4`), modelos retirados de Gemini/Anthropic. Actualizados.
16. El listado de modelos mandaba `Authorization: Bearer` a todos; Anthropic (`x-api-key`)
    y Gemini lo rechazaban. Ahora cada proveedor lista con su propia autenticación,
    logins incluidos.
17. El cliente HTTP del LLM ignoraba `HTTPS_PROXY`; ahora usa `ProxyFromEnvironment`.
18. El `.env` generado decía `source MOTITA_LLM_API_KEY` en lugar del archivo.

## Cómo autentica cada proveedor ahora

| `llm.provider` | API key | Login con cuenta | Endpoint |
|---|---|---|---|
| `openai` | `OPENAI_API_KEY` / `MOTITA_LLM_API_KEY` | — | `/chat/completions` (cualquier host compatible) |
| `codex` | `OPENAI_API_KEY` / `MOTITA_LLM_API_KEY` | Sign in with ChatGPT (PKCE, callback en `localhost:1455` o pegado) | `/responses` |
| `copilot` | `GITHUB_COPILOT_TOKEN` (token OAuth de GitHub) | Device code de GitHub | `/chat/completions` del plan |
| `ollama` | `OLLAMA_API_KEY` (solo Cloud) | — | local `http://localhost:11434/v1` o `https://ollama.com/v1` |
| `anthropic` | `ANTHROPIC_API_KEY` | — | `/v1/messages` |
| `claude-code` | — | `claude auth login` (CLI oficial) | CLI `claude` |
| `gemini` | `GEMINI_API_KEY` | Google OAuth con cliente propio, o ADC de gcloud | `generateContent` |
| `qwen` | `DASHSCOPE_API_KEY` | Device code de qwen.ai | DashScope / host de `resource_url` |

Prioridad: una API key configurada siempre gana sobre un login guardado.

## Verificación

- Tests nuevos: `internal/llm/providers_test.go` (cada proveedor y modo de autenticación
  contra servidores simulados), `internal/oauth/flows_test.go` (PKCE, almacén, callback,
  Codex, Qwen, Gemini, Copilot), tests del wizard y de `config`, y del cambio de
  proveedor en `internal/tui`.
- `go vet ./...` y `gofmt` limpios. `go test ./...` pasa salvo `internal/sandbox`,
  `internal/webui` e `internal/gitx`, que ya fallaban antes de este cambio por el entorno
  (sin cgroups, assets web sin compilar, identidad git global del contenedor).

## Pendiente (no verificable desde aquí)

- **Ninguna prueba contra los servicios reales:** este entorno no tiene cuentas ni salida a
  esas APIs. Hay que probar un login real de cada proveedor. Lo más incierto:
  - el backend de ChatGPT para Codex: si exige `instructions` concretas o rechaza algún
    campo (no se envía `max_output_tokens` ni `temperature`);
  - los endpoints de Qwen (`chat.qwen.ai/api/v1/oauth2/*`) y si el acceso gratuito por
    OAuth sigue disponible;
  - si Google acepta los scopes de la Gemini API con el cliente por defecto de `gcloud`
    (la documentación de Gemini pide `--client-id-file` con un cliente propio).
- **Términos de servicio:** Copilot (client id de VS Code y endpoint interno
  `copilot_internal`), Codex (client id de Codex CLI) y Qwen (client id de Qwen Code)
  reutilizan identidades de clientes oficiales, como hacen otras herramientas de terceros.
  Conviene confirmar que los términos vigentes de cada proveedor lo permiten.
- El cliente HTTP de los flujos OAuth no usa el bundle de CAs embebido que sí usa el
  cliente del LLM (relevante en el netbook i386 con CAs antiguas).
- `llm.reasoning` aún no se traduce a `thinking` (Anthropic) ni a `thinkingBudget` (Gemini),
  aunque `config.go` lo documenta.
