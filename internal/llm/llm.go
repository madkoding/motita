// Package llm implementa la Capa B: el motor de razonamiento.
//
// Es un cliente ligero, sin SDK, que habla con tres familias de API:
//
//   - openai:    POST /chat/completions  (Bearer)
//   - anthropic: POST /v1/messages       (x-api-key + anthropic-version)
//   - gemini:    POST /v1beta/models/<modelo>:generateContent (?key=)
//
// Todos los proveedores se normalizan a la misma estructura de mensajes y a
// texto plano de vuelta, para que el bucle del agente no sepa cuál está detrás.
// El parseo de respuestas estructuradas y el reintento con backoff exponencial
// viven aquí.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
)

// Mensaje es un turno de la conversación.
type Mensaje struct {
	Rol       string // system | user | assistant
	Contenido string
}

// Cliente habla con el proveedor configurado.
type Cliente struct {
	cfg    config.LLM
	http   *http.Client
	log    *logx.Logger
	dormir func(time.Duration) // inyectable para que las pruebas no esperen
}

// Nuevo crea el cliente del motor de razonamiento.
func Nuevo(cfg config.LLM, log *logx.Logger) (*Cliente, error) {
	if log == nil {
		log = logx.Global()
	}
	switch strings.ToLower(cfg.Proveedor) {
	case "openai", "anthropic", "gemini":
	default:
		return nil, fmt.Errorf("proveedor de LLM no soportado: %q", cfg.Proveedor)
	}
	if cfg.APIKey == "" {
		return nil, errors.New("falta la clave del LLM")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 90 * time.Second
	}
	if cfg.MaxIntentos < 1 {
		cfg.MaxIntentos = 1
	}
	if cfg.BackoffInicial <= 0 {
		cfg.BackoffInicial = time.Second
	}
	if cfg.BackoffMax < cfg.BackoffInicial {
		cfg.BackoffMax = cfg.BackoffInicial
	}

	return &Cliente{
		cfg:  cfg,
		http: &http.Client{Timeout: cfg.Timeout},
		log:  log,
		dormir: func(d time.Duration) {
			time.Sleep(d)
		},
	}, nil
}

// Completar envía la conversación y devuelve el texto del modelo, reintentando
// con backoff exponencial ante fallos transitorios.
func (c *Cliente) Completar(ctx context.Context, mensajes []Mensaje) (string, error) {
	var ultimo error
	espera := c.cfg.BackoffInicial

	for intento := 1; intento <= c.cfg.MaxIntentos; intento++ {
		texto, err := c.llamada(ctx, mensajes)
		if err == nil {
			return texto, nil
		}
		ultimo = err

		if !reintentable(err) {
			// Un error de credenciales o de petición no mejora reintentando:
			// se falla rápido en lugar de gastar tiempo y cuota.
			c.log.Error("llamada al LLM fallida sin posibilidad de reintento",
				"intento", intento, "error", err)
			return "", err
		}
		if intento == c.cfg.MaxIntentos {
			break
		}

		c.log.Warn("reintentando llamada al LLM",
			"intento", intento, "max_intentos", c.cfg.MaxIntentos,
			"espera", espera.String(), "error", err)

		select {
		case <-ctx.Done():
			return "", fmt.Errorf("cancelado mientras se esperaba para reintentar: %w", ctx.Err())
		case <-time.After(espera):
		}
		espera *= 2
		if espera > c.cfg.BackoffMax {
			espera = c.cfg.BackoffMax
		}
	}

	return "", fmt.Errorf("se agotaron los %d intentos: %w", c.cfg.MaxIntentos, ultimo)
}

// ErrorHTTP describe un fallo con código para decidir si es reintentable.
type ErrorHTTP struct {
	Codigo int
	Cuerpo string
}

func (e *ErrorHTTP) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.Codigo, recortar(e.Cuerpo, 400))
}

// reintentable indica si merece la pena volver a intentarlo.
func reintentable(err error) bool {
	var eh *ErrorHTTP
	if errors.As(err, &eh) {
		switch {
		case eh.Codigo == 429, eh.Codigo >= 500:
			return true
		default:
			return false
		}
	}
	// Errores de red (timeouts, DNS, conexión cortada) sí se reintentan.
	return true
}

// llamada hace una única petición al proveedor.
func (c *Cliente) llamada(ctx context.Context, mensajes []Mensaje) (string, error) {
	switch strings.ToLower(c.cfg.Proveedor) {
	case "anthropic":
		return c.llamadaAnthropic(ctx, mensajes)
	case "gemini":
		return c.llamadaGemini(ctx, mensajes)
	default:
		return c.llamadaOpenAI(ctx, mensajes)
	}
}

func (c *Cliente) baseURL(defecto string) string {
	if c.cfg.BaseURL == "" {
		return defecto
	}
	return strings.TrimRight(c.cfg.BaseURL, "/")
}

// --- OpenAI ----------------------------------------------------------------

type openAIMensaje struct {
	Rol       string `json:"role"`
	Contenido string `json:"content"`
}

type openAIRespuesta struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Cliente) llamadaOpenAI(ctx context.Context, mensajes []Mensaje) (string, error) {
	cuerpo := map[string]any{
		"model":       c.cfg.Modelo,
		"messages":    aOpenAI(mensajes),
		"max_tokens":  c.cfg.MaxTokens,
		"temperature": c.cfg.Temperature,
	}
	url := c.baseURL("https://api.openai.com/v1") + "/chat/completions"
	cabeceras := map[string]string{"Authorization": "Bearer " + c.cfg.APIKey}

	datos, err := c.post(ctx, url, cabeceras, cuerpo)
	if err != nil {
		return "", err
	}
	var resp openAIRespuesta
	if err := json.Unmarshal(datos, &resp); err != nil {
		return "", fmt.Errorf("respuesta de OpenAI ilegible: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return "", &ErrorHTTP{Codigo: 400, Cuerpo: resp.Error.Message}
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("OpenAI devolvió choices vacío")
	}
	return resp.Choices[0].Message.Content, nil
}

func aOpenAI(mensajes []Mensaje) []openAIMensaje {
	salida := make([]openAIMensaje, 0, len(mensajes))
	for _, m := range mensajes {
		rol := m.Rol
		if rol == "" {
			rol = "user"
		}
		salida = append(salida, openAIMensaje{Rol: rol, Contenido: m.Contenido})
	}
	return salida
}

// --- Anthropic --------------------------------------------------------------

type anthropicRespuesta struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Cliente) llamadaAnthropic(ctx context.Context, mensajes []Mensaje) (string, error) {
	// Anthropic recibe el sistema aparte y no admite el rol "system" en la lista.
	var sistema string
	conversacion := make([]map[string]any, 0, len(mensajes))
	for _, m := range mensajes {
		if m.Rol == "system" {
			if sistema != "" {
				sistema += "\n\n"
			}
			sistema += m.Contenido
			continue
		}
		rol := m.Rol
		if rol != "assistant" {
			rol = "user"
		}
		conversacion = append(conversacion, map[string]any{
			"role": rol,
			"content": []map[string]any{
				{"type": "text", "text": m.Contenido},
			},
		})
	}

	cuerpo := map[string]any{
		"model":       c.cfg.Modelo,
		"messages":    conversacion,
		"max_tokens":  max(c.cfg.MaxTokens, 1),
		"temperature": c.cfg.Temperature,
	}
	if sistema != "" {
		cuerpo["system"] = sistema
	}

	url := c.baseURL("https://api.anthropic.com") + "/v1/messages"
	cabeceras := map[string]string{
		"x-api-key":         c.cfg.APIKey,
		"anthropic-version": "2023-06-01",
	}

	datos, err := c.post(ctx, url, cabeceras, cuerpo)
	if err != nil {
		return "", err
	}
	var resp anthropicRespuesta
	if err := json.Unmarshal(datos, &resp); err != nil {
		return "", fmt.Errorf("respuesta de Anthropic ilegible: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return "", &ErrorHTTP{Codigo: 400, Cuerpo: resp.Error.Message}
	}
	var sb strings.Builder
	for _, parte := range resp.Content {
		if parte.Type == "text" || parte.Type == "" {
			sb.WriteString(parte.Text)
		}
	}
	if sb.Len() == 0 {
		return "", errors.New("Anthropic devolvió una respuesta sin texto")
	}
	return sb.String(), nil
}

// --- Gemini -----------------------------------------------------------------

type geminiRespuesta struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	} `json:"candidates"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (c *Cliente) llamadaGemini(ctx context.Context, mensajes []Mensaje) (string, error) {
	var sistema string
	var contenidos []map[string]any
	for _, m := range mensajes {
		if m.Rol == "system" {
			sistema += m.Contenido + "\n"
			continue
		}
		rol := "user"
		if m.Rol == "assistant" {
			rol = "model"
		}
		contenidos = append(contenidos, map[string]any{
			"role":  rol,
			"parts": []map[string]any{{"text": m.Contenido}},
		})
	}

	cuerpo := map[string]any{
		"contents": contenidos,
		"generationConfig": map[string]any{
			"maxOutputTokens": c.cfg.MaxTokens,
			"temperature":     c.cfg.Temperature,
		},
	}
	if sistema != "" {
		cuerpo["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": sistema}},
		}
	}

	base := c.baseURL("https://generativelanguage.googleapis.com")
	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", base, c.cfg.Modelo, c.cfg.APIKey)

	datos, err := c.post(ctx, url, nil, cuerpo)
	if err != nil {
		return "", err
	}
	var resp geminiRespuesta
	if err := json.Unmarshal(datos, &resp); err != nil {
		return "", fmt.Errorf("respuesta de Gemini ilegible: %w", err)
	}
	if resp.Error != nil && resp.Error.Message != "" {
		return "", &ErrorHTTP{Codigo: 400, Cuerpo: resp.Error.Message}
	}
	if len(resp.Candidates) == 0 {
		return "", errors.New("Gemini devolvió una respuesta sin candidatos (¿bloqueo de seguridad?)")
	}
	var sb strings.Builder
	for _, parte := range resp.Candidates[0].Content.Parts {
		sb.WriteString(parte.Text)
	}
	if sb.Len() == 0 {
		return "", fmt.Errorf("Gemini devolvió una respuesta vacía (finishReason=%q)", resp.Candidates[0].FinishReason)
	}
	return sb.String(), nil
}

// --- transporte -------------------------------------------------------------

func (c *Cliente) post(ctx context.Context, url string, cabeceras map[string]string, cuerpo any) ([]byte, error) {
	datos, err := json.Marshal(cuerpo)
	if err != nil {
		return nil, fmt.Errorf("no se pudo serializar la petición: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(datos))
	if err != nil {
		return nil, fmt.Errorf("petición inválida: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cabeceras {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err // error de red: reintentable
	}
	defer resp.Body.Close()

	respuesta, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &ErrorHTTP{Codigo: resp.StatusCode, Cuerpo: strings.TrimSpace(string(respuesta))}
	}
	return respuesta, nil
}

// recortar limita un texto para los mensajes de error.
func recortar(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- Respuestas estructuradas ----------------------------------------------

var bloqueJSON = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{.*?\\}|\\[.*?\\])\\s*```")

// ExtraerJSON obtiene el primer objeto JSON de una respuesta de LLM, tolerando
// los adornos habituales: bloques markdown, texto antes y después, y llaves
// anidadas.
func ExtraerJSON(texto string) ([]byte, error) {
	limpio := strings.TrimSpace(texto)
	if limpio == "" {
		return nil, errors.New("la respuesta del LLM está vacía")
	}

	if m := bloqueJSON.FindStringSubmatch(limpio); len(m) == 2 {
		return []byte(m[1]), nil
	}

	// Primer '{' y su pareja balanceada, respetando cadenas y escapes.
	inicio := strings.IndexAny(limpio, "{[")
	if inicio < 0 {
		return nil, fmt.Errorf("la respuesta no contiene JSON: %q", recortar(limpio, 200))
	}
	abre := limpio[inicio]
	cierra := byte('}')
	if abre == '[' {
		cierra = ']'
	}

	profundidad := 0
	enCadena := false
	escapado := false
	for i := inicio; i < len(limpio); i++ {
		ch := limpio[i]
		switch {
		case escapado:
			escapado = false
		case ch == '\\' && enCadena:
			escapado = true
		case ch == '"':
			enCadena = !enCadena
		case enCadena:
			// nada
		case ch == abre:
			profundidad++
		case ch == cierra:
			profundidad--
			if profundidad == 0 {
				return []byte(limpio[inicio : i+1]), nil
			}
		}
	}
	return nil, fmt.Errorf("el JSON de la respuesta está truncado: %q", recortar(limpio, 200))
}

// DecodificarJSON extrae y deserializa en destino, con errores explicables.
func DecodificarJSON(texto string, destino any) error {
	crudo, err := ExtraerJSON(texto)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(crudo, destino); err != nil {
		return fmt.Errorf("el JSON de la respuesta no encaja con lo esperado (%v): %s", err, recortar(string(crudo), 300))
	}
	return nil
}
