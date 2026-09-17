package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
)

func init() {
	l, _ := logx.Nuevo(logx.Opciones{Nivel: logx.Error, Consola: false})
	logx.Instalar(l)
}

func clienteDePrueba(t *testing.T, proveedor, url string, cfg func(*config.LLM)) *Cliente {
	t.Helper()
	c := config.LLM{
		Proveedor:      proveedor,
		Modelo:         "modelo-de-prueba",
		APIKey:         "clave",
		BaseURL:        url,
		MaxTokens:      100,
		Temperature:    0.1,
		Timeout:        5 * time.Second,
		MaxIntentos:    1,
		BackoffInicial: time.Millisecond,
		BackoffMax:     2 * time.Millisecond,
	}
	if cfg != nil {
		cfg(&c)
	}
	cli, err := Nuevo(c, logx.Global())
	if err != nil {
		t.Fatalf("no se pudo crear el cliente: %v", err)
	}
	return cli
}

// --- OpenAI -----------------------------------------------------------------

func TestOpenAIOK(t *testing.T) {
	var recibido map[string]any
	var cabecera string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&recibido)
		cabecera = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"respuesta de prueba"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()

	c := clienteDePrueba(t, "openai", srv.URL, nil)
	texto, err := c.Completar(context.Background(), []Mensaje{
		{Rol: "system", Contenido: "eres un agente"},
		{Rol: "user", Contenido: "hola"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if texto != "respuesta de prueba" {
		t.Errorf("texto = %q", texto)
	}
	if cabecera != "Bearer clave" {
		t.Errorf("cabecera de autorización = %q", cabecera)
	}
	if recibido["model"] != "modelo-de-prueba" {
		t.Errorf("modelo enviado = %v", recibido["model"])
	}
	mensajes := recibido["messages"].([]any)
	if len(mensajes) != 2 {
		t.Errorf("mensajes enviados = %d", len(mensajes))
	}
}

// --- Anthropic --------------------------------------------------------------

func TestAnthropicOK(t *testing.T) {
	var recibido map[string]any
	var cabeceraClave, cabeceraVersion string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&recibido)
		cabeceraClave = r.Header.Get("x-api-key")
		cabeceraVersion = r.Header.Get("anthropic-version")
		fmt.Fprint(w, `{"content":[{"type":"text","text":"respuesta anthropic"}]}`)
	}))
	defer srv.Close()

	c := clienteDePrueba(t, "anthropic", srv.URL, nil)
	texto, err := c.Completar(context.Background(), []Mensaje{
		{Rol: "system", Contenido: "sistema"},
		{Rol: "user", Contenido: "hola"},
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if texto != "respuesta anthropic" {
		t.Errorf("texto = %q", texto)
	}
	if cabeceraClave != "clave" {
		t.Errorf("x-api-key = %q", cabeceraClave)
	}
	if cabeceraVersion == "" {
		t.Error("falta la cabecera anthropic-version")
	}
	// El sistema va aparte, no como mensaje.
	if recibido["system"] != "sistema" {
		t.Errorf("system = %v", recibido["system"])
	}
	if len(recibido["messages"].([]any)) != 1 {
		t.Errorf("los mensajes no deben incluir el system: %v", recibido["messages"])
	}
}

// --- Gemini -----------------------------------------------------------------

func TestGeminiOK(t *testing.T) {
	var ruta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ruta = r.URL.String()
		fmt.Fprint(w, `{"candidates":[{"content":{"parts":[{"text":"respuesta gemini"}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	c := clienteDePrueba(t, "gemini", srv.URL, nil)
	texto, err := c.Completar(context.Background(), []Mensaje{{Rol: "user", Contenido: "hola"}})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if texto != "respuesta gemini" {
		t.Errorf("texto = %q", texto)
	}
	if !strings.Contains(ruta, "generateContent") || !strings.Contains(ruta, "key=clave") {
		t.Errorf("ruta inesperada: %q", ruta)
	}
}

// --- Reintentos -------------------------------------------------------------

// TestReintentaAnte500: los errores de servidor se reintentan con backoff.
func TestReintentaAnte500(t *testing.T) {
	intentos := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		intentos++
		if intentos < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"error":{"message":"fallo temporal"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"al tercer intento"}}]}`)
	}))
	defer srv.Close()

	c := clienteDePrueba(t, "openai", srv.URL, func(c *config.LLM) {
		c.MaxIntentos = 5
	})
	texto, err := c.Completar(context.Background(), []Mensaje{{Rol: "user", Contenido: "x"}})
	if err != nil {
		t.Fatalf("debería haber triunfado al tercer intento: %v", err)
	}
	if texto != "al tercer intento" {
		t.Errorf("texto = %q", texto)
	}
	if intentos != 3 {
		t.Errorf("intentos = %d", intentos)
	}
}

// TestReintentaAnte429 y NO ante 401: reintentar una credencial inválida es
// gastar tiempo y cuota para nada.
func TestNoReintentaCredencialesInvalidas(t *testing.T) {
	intentos := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		intentos++
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":{"message":"clave inválida"}}`)
	}))
	defer srv.Close()

	c := clienteDePrueba(t, "openai", srv.URL, func(c *config.LLM) { c.MaxIntentos = 5 })
	_, err := c.Completar(context.Background(), []Mensaje{{Rol: "user", Contenido: "x"}})
	if err == nil {
		t.Fatal("se esperaba un error")
	}
	if intentos != 1 {
		t.Errorf("un 401 no debe reintentarse: hubo %d intentos", intentos)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("el error debe mencionar el código: %v", err)
	}
}

func TestReintentaAnte429(t *testing.T) {
	intentos := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		intentos++
		if intentos == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"error":{"message":"demasiadas peticiones"}}`)
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
	}))
	defer srv.Close()

	c := clienteDePrueba(t, "openai", srv.URL, func(c *config.LLM) { c.MaxIntentos = 3 })
	texto, err := c.Completar(context.Background(), []Mensaje{{Rol: "user", Contenido: "x"}})
	if err != nil {
		t.Fatalf("debería reintentar un 429: %v", err)
	}
	if texto != "ok" || intentos != 2 {
		t.Errorf("texto=%q intentos=%d", texto, intentos)
	}
}

func TestAgotaIntentos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "puerta de enlace caída")
	}))
	defer srv.Close()

	c := clienteDePrueba(t, "openai", srv.URL, func(c *config.LLM) { c.MaxIntentos = 3 })
	_, err := c.Completar(context.Background(), []Mensaje{{Rol: "user", Contenido: "x"}})
	if err == nil || !strings.Contains(err.Error(), "se agotaron los 3 intentos") {
		t.Fatalf("error = %v", err)
	}
}

// --- Casos límite -----------------------------------------------------------

func TestChoicesVacioNoEntraEnPanico(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"choices":[]}`)
	}))
	defer srv.Close()

	c := clienteDePrueba(t, "openai", srv.URL, nil)
	if _, err := c.Completar(context.Background(), []Mensaje{{Rol: "user", Contenido: "x"}}); err == nil {
		t.Fatal("choices vacío debe ser un error, no un pánico")
	}
}

func TestCuerpoNoJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "<html>bad gateway</html>")
	}))
	defer srv.Close()

	c := clienteDePrueba(t, "openai", srv.URL, nil)
	_, err := c.Completar(context.Background(), []Mensaje{{Rol: "user", Contenido: "x"}})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("error = %v", err)
	}
}

func TestContextoCancelado(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()

	c := clienteDePrueba(t, "openai", srv.URL, func(c *config.LLM) { c.MaxIntentos = 5 })
	ctx, cancelar := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelar()

	inicio := time.Now()
	if _, err := c.Completar(ctx, []Mensaje{{Rol: "user", Contenido: "x"}}); err == nil {
		t.Fatal("se esperaba un error por contexto cancelado")
	}
	if transcurrido := time.Since(inicio); transcurrido > 3*time.Second {
		t.Errorf("la cancelación no se respetó: %s", transcurrido)
	}
}

func TestValidacionDeConfiguracion(t *testing.T) {
	if _, err := Nuevo(config.LLM{Proveedor: "mago", APIKey: "x"}, logx.Global()); err == nil {
		t.Error("un proveedor desconocido debe fallar")
	}
	if _, err := Nuevo(config.LLM{Proveedor: "openai"}, logx.Global()); err == nil {
		t.Error("sin clave debe fallar")
	}
}

// --- Parseo de JSON del LLM -------------------------------------------------

// TestExtraerJSON cubre lo que de verdad devuelven los modelos: bloques
// markdown, texto alrededor y llaves anidadas.
func TestExtraerJSON(t *testing.T) {
	casos := []struct {
		nombre   string
		entrada  string
		esperado string
		falla    bool
	}{
		{"json limpio", `{"a":1}`, `{"a":1}`, false},
		{"bloque markdown", "Claro:\n```json\n{\"a\":1}\n```\nListo.", `{"a":1}`, false},
		{"texto alrededor", `Mi respuesta es {"a":{"b":2}} y ya está.`, `{"a":{"b":2}}`, false},
		{"con llaves en cadena", `{"a":"texto con } dentro"}`, `{"a":"texto con } dentro"}`, false},
		{"con escapes", `{"a":"comilla \" y llave } dentro"}`, `{"a":"comilla \" y llave } dentro"}`, false},
		{"lista", `[{"a":1},{"b":2}]`, `[{"a":1},{"b":2}]`, false},
		{"sin json", "lo siento, no puedo", "", true},
		{"truncado", `{"a":1`, "", true},
		{"vacío", "", "", true},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			crudo, err := ExtraerJSON(caso.entrada)
			if caso.falla {
				if err == nil {
					t.Fatalf("se esperaba error y se obtuvo %s", crudo)
				}
				return
			}
			if err != nil {
				t.Fatalf("error inesperado: %v", err)
			}
			if string(crudo) != caso.esperado {
				t.Errorf("extraído = %s, se esperaba %s", crudo, caso.esperado)
			}
		})
	}
}

func TestDecodificarJSONEnEstructura(t *testing.T) {
	var destino struct {
		Plan []struct {
			Paso    int    `json:"paso"`
			Accion  string `json:"accion"`
			Comando string `json:"comando"`
		} `json:"plan"`
	}
	entrada := "Aquí tienes:\n```json\n{\"plan\":[{\"paso\":1,\"accion\":\"listar\",\"comando\":\"ls -la\"}]}\n```"
	if err := DecodificarJSON(entrada, &destino); err != nil {
		t.Fatalf("error: %v", err)
	}
	if len(destino.Plan) != 1 || destino.Plan[0].Comando != "ls -la" {
		t.Errorf("decodificado = %+v", destino)
	}
}
