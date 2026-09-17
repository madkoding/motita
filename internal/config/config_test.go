package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseYAMLBasico(t *testing.T) {
	datos := []byte(`
# comentario
nombre: agente
version: 2
ratio: 0.5
activo: true
desactivado: false
nada: ~
lista:
  - uno
  - dos
en_linea: [a, b, c]
mapa:
  clave: valor
  anidado:
    profundo: si
mapa_en_linea: {x: 1, y: 2}
`)
	m, err := ParseYAML(datos)
	if err != nil {
		t.Fatalf("ParseYAML falló: %v", err)
	}
	if m["nombre"] != "agente" {
		t.Errorf("nombre = %v", m["nombre"])
	}
	if m["version"] != int64(2) {
		t.Errorf("version = %v (%T)", m["version"], m["version"])
	}
	if m["ratio"] != 0.5 {
		t.Errorf("ratio = %v (%T)", m["ratio"], m["ratio"])
	}
	if m["activo"] != true || m["desactivado"] != false {
		t.Errorf("booleanos mal: activo=%v desactivado=%v", m["activo"], m["desactivado"])
	}
	if m["nada"] != nil {
		t.Errorf("~ debería ser nil, es %v", m["nada"])
	}
	lista, ok := m["lista"].([]any)
	if !ok || len(lista) != 2 || lista[0] != "uno" {
		t.Errorf("lista = %#v", m["lista"])
	}
	enLinea, ok := m["en_linea"].([]any)
	if !ok || len(enLinea) != 3 {
		t.Errorf("en_linea = %#v", m["en_linea"])
	}
	mapa, ok := m["mapa"].(map[string]any)
	if !ok {
		t.Fatalf("mapa = %#v", m["mapa"])
	}
	anidado, ok := mapa["anidado"].(map[string]any)
	if !ok || anidado["profundo"] != "si" {
		t.Errorf("mapa.anidado = %#v", mapa["anidado"])
	}
	if m["mapa_en_linea"].(map[string]any)["x"] != int64(1) {
		t.Errorf("mapa_en_linea = %#v", m["mapa_en_linea"])
	}
}

func TestParseYAMLComillasYComentarios(t *testing.T) {
	datos := []byte(`
simple: 'con # almohadilla dentro'
doble: "con \"escape\" y salto\n"
con_comentario: valor   # esto se ignora
vacio: ""
`)
	m, err := ParseYAML(datos)
	if err != nil {
		t.Fatalf("ParseYAML falló: %v", err)
	}
	if m["simple"] != "con # almohadilla dentro" {
		t.Errorf("simple = %q", m["simple"])
	}
	if m["doble"] != "con \"escape\" y salto\n" {
		t.Errorf("doble = %q", m["doble"])
	}
	if m["con_comentario"] != "valor" {
		t.Errorf("con_comentario = %q", m["con_comentario"])
	}
	if m["vacio"] != "" {
		t.Errorf("vacio = %q", m["vacio"])
	}
}

func TestParseYAMLEscalaresDeBloque(t *testing.T) {
	datos := []byte(`
literal: |
  linea uno
  linea dos

  tras vacio
plegado: >
  esto se
  une en una linea
sin_salto: |-
  sin salto final
`)
	m, err := ParseYAML(datos)
	if err != nil {
		t.Fatalf("ParseYAML falló: %v", err)
	}
	literal, _ := m["literal"].(string)
	if !strings.Contains(literal, "linea uno\nlinea dos") {
		t.Errorf("literal = %q", literal)
	}
	if !strings.Contains(literal, "tras vacio") {
		t.Errorf("la línea tras el vacío se perdió: %q", literal)
	}
	plegado, _ := m["plegado"].(string)
	if !strings.Contains(plegado, "esto se une en una linea") {
		t.Errorf("plegado = %q", plegado)
	}
	sinSalto, _ := m["sin_salto"].(string)
	if strings.HasSuffix(sinSalto, "\n") {
		t.Errorf("|- no debe acabar en salto: %q", sinSalto)
	}
}

func TestParseYAMLListaDeMapas(t *testing.T) {
	datos := []byte(`
checks:
  - nombre: primero
    comando: make test
    esperar_exit: 0
  - nombre: segundo
    comando: ./lint.sh
`)
	m, err := ParseYAML(datos)
	if err != nil {
		t.Fatalf("ParseYAML falló: %v", err)
	}
	lista, ok := m["checks"].([]any)
	if !ok || len(lista) != 2 {
		t.Fatalf("checks = %#v", m["checks"])
	}
	primero := lista[0].(map[string]any)
	if primero["nombre"] != "primero" || primero["comando"] != "make test" || primero["esperar_exit"] != int64(0) {
		t.Errorf("primer check = %#v", primero)
	}
	segundo := lista[1].(map[string]any)
	if segundo["nombre"] != "segundo" {
		t.Errorf("segundo check = %#v", segundo)
	}
}

func TestParseYAMLErroresExplicitos(t *testing.T) {
	casos := []struct {
		nombre   string
		yaml     string
		contiene string
	}{
		{"tabuladores", "a:\n\tb: 1\n", "tabuladores"},
		{"clave sin valor", "a\n", "se esperaba 'clave: valor'"},
		{"raíz secuencia", "- uno\n- dos\n", "debe ser un mapa"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			_, err := ParseYAML([]byte(caso.yaml))
			if err == nil {
				t.Fatalf("se esperaba error para %q", caso.yaml)
			}
			if !strings.Contains(err.Error(), caso.contiene) {
				t.Errorf("error = %q, se esperaba que contuviera %q", err, caso.contiene)
			}
		})
	}
}

// TestCargarEjemploDelRepo valida que el YAML de ejemplo es correcto: si se
// rompe la documentación, falla la prueba.
func TestCargarEjemploDelRepo(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "clave-de-prueba")

	ruta := filepath.Join("..", "..", "configs", "agent.yaml.example")
	if _, err := os.Stat(ruta); err != nil {
		t.Skipf("no está el ejemplo en %s: %v", ruta, err)
	}

	cfg, err := Cargar(ruta)
	if err != nil {
		t.Fatalf("el ejemplo del repositorio no carga: %v", err)
	}
	if cfg.LLM.Modelo == "" || cfg.Agent.MaxReintentos == 0 {
		t.Errorf("configuración incompleta: %+v", cfg)
	}
	if cfg.Anchor.Tipo == "command" && cfg.Anchor.Comando == "" {
		t.Error("ancla de tipo command sin comando")
	}
}

func TestCargarCasosDeUso(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "clave-de-prueba")

	for _, caso := range []string{"1-desarrollo.yaml", "2-datos.yaml", "3-automatizacion.yaml"} {
		t.Run(caso, func(t *testing.T) {
			ruta := filepath.Join("..", "..", "configs", "casos", caso)
			if _, err := os.Stat(ruta); err != nil {
				t.Skipf("no está %s: %v", ruta, err)
			}
			cfg, err := Cargar(ruta)
			if err != nil {
				t.Fatalf("el caso %s no carga: %v", caso, err)
			}
			// Cada caso debe tener el flujo completo definido.
			if cfg.Prompts.Analyze.Usuario == "" || cfg.Prompts.Plan.Usuario == "" || cfg.Prompts.Execute.Usuario == "" {
				t.Errorf("el caso %s no define las tres plantillas de prompt", caso)
			}
			if cfg.Anchor.Tipo == "none" {
				t.Errorf("el caso %s no valida nada (anchor.tipo=none)", caso)
			}
			if cfg.FinalAction.Tipo == "none" {
				t.Errorf("el caso %s no define acción final", caso)
			}
		})
	}
}

func TestValidarDetectaErrores(t *testing.T) {
	casos := []struct {
		nombre    string
		modificar func(*Config)
		contiene  string
	}{
		{"fuente desconocida", func(c *Config) { c.TaskSource.Tipo = "telepatia" }, "task_source.tipo desconocido"},
		{"file sin ruta", func(c *Config) { c.TaskSource.Tipo = "file" }, "requiere 'ruta'"},
		{"api sin url", func(c *Config) { c.TaskSource.Tipo = "api" }, "requiere 'url'"},
		{"ancla command sin comando", func(c *Config) { c.Anchor.Tipo = "command" }, "requiere 'comando'"},
		{"sandbox chroot sin raíz", func(c *Config) { c.Sandbox.Tipo = "chroot" }, "requiere 'raiz'"},
		{"proveedor desconocido", func(c *Config) { c.LLM.Proveedor = "mago" }, "llm.proveedor desconocido"},
		{"sin clave", func(c *Config) { c.LLM.APIKey = "" }, "falta la clave del LLM"},
		{"final api sin url", func(c *Config) { c.FinalAction.Tipo = "api" }, "requiere 'url'"},
		{"nivel de log", func(c *Config) { c.Agent.LogNivel = "verboso" }, "agent.log_level desconocido"},
		{"usuario inválido", func(c *Config) { c.Sandbox.Usuario = "a:b:c" }, "uid:gid"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			cfg := Defecto()
			cfg.LLM.APIKey = "x"
			caso.modificar(&cfg)
			err := cfg.Validar()
			if err == nil {
				t.Fatal("se esperaba un error de validación")
			}
			if !strings.Contains(err.Error(), caso.contiene) {
				t.Errorf("error = %q, se esperaba que contuviera %q", err, caso.contiene)
			}
		})
	}
}

func TestDecodificarClaveDesconocida(t *testing.T) {
	m, err := ParseYAML([]byte("llm:\n  modelo: x\n  color_favorito: azul\n"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	err = Decodificar(m, &cfg)
	if err == nil {
		t.Fatal("una clave desconocida debe ser un error, no un silencio")
	}
	if !strings.Contains(err.Error(), "color_favorito") || !strings.Contains(err.Error(), "llm") {
		t.Errorf("el error debe indicar la clave y su bloque: %q", err)
	}
}

func TestDecodificarDuraciones(t *testing.T) {
	m, err := ParseYAML([]byte(`
anchor:
  timeout: 45s
llm:
  timeout: 90
sandbox:
  timeout: 5m
`))
	if err != nil {
		t.Fatal(err)
	}
	var cfg Config
	if err := Decodificar(m, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Anchor.Timeout != 45*time.Second {
		t.Errorf("anchor.timeout = %v", cfg.Anchor.Timeout)
	}
	if cfg.LLM.Timeout != 90*time.Second {
		t.Errorf("un número sin sufijo debe ser segundos: %v", cfg.LLM.Timeout)
	}
	if cfg.Sandbox.Timeout != 5*time.Minute {
		t.Errorf("sandbox.timeout = %v", cfg.Sandbox.Timeout)
	}
}

func TestEntornoGanaSobreYAML(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_MODELO", "modelo-de-entorno")
	t.Setenv("STARLIGHT_AGENT_MAX_REINTENTOS", "7")
	t.Setenv("STARLIGHT_SANDBOX_AISLAR_RED", "true")
	t.Setenv("STARLIGHT_LLM_API_KEY", "clave")

	cfg := Defecto()
	if err := AplicarEntorno(&cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Modelo != "modelo-de-entorno" {
		t.Errorf("modelo = %q", cfg.LLM.Modelo)
	}
	if cfg.Agent.MaxReintentos != 7 {
		t.Errorf("max_reintentos = %d", cfg.Agent.MaxReintentos)
	}
	if !cfg.Sandbox.AislarRed {
		t.Error("aislar_red debería ser true")
	}
}

func TestEntornoInvalido(t *testing.T) {
	t.Setenv("STARLIGHT_LLM_API_KEY", "x")
	t.Setenv("STARLIGHT_AGENT_MAX_REINTENTOS", "muchos")
	cfg := Defecto()
	if err := AplicarEntorno(&cfg); err == nil {
		t.Fatal("un entero inválido en el entorno debe dar error")
	}
}

func TestParsearUsuario(t *testing.T) {
	uid, gid, err := ParsearUsuario("1000:1001")
	if err != nil || uid != 1000 || gid != 1001 {
		t.Errorf("1000:1001 -> %d %d %v", uid, gid, err)
	}
	uid, gid, err = ParsearUsuario("1000")
	if err != nil || uid != 1000 || gid != 1000 {
		t.Errorf("1000 -> %d %d %v", uid, gid, err)
	}
	if _, _, err := ParsearUsuario("mil"); err == nil {
		t.Error("un usuario no numérico debe fallar")
	}
}
