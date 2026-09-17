// Package config define la configuración del agente y su carga desde YAML y
// variables de entorno, sin dependencias externas.
//
// El agente es 100% configurable sin recompilar: ninguna decisión de negocio
// (fuente de tareas, validador, límites del sandbox, proveedor de LLM,
// plantillas de prompts ni acción final) vive en el código.
package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Estructuras (una por bloque del YAML)
// ---------------------------------------------------------------------------

// Config es la raíz del archivo de configuración.
type Config struct {
	TaskSource  TaskSource  `yaml:"task_source"`
	Anchor      Anchor      `yaml:"anchor"`
	Sandbox     Sandbox     `yaml:"sandbox"`
	LLM         LLM         `yaml:"llm"`
	Prompts     Prompts     `yaml:"prompts"`
	FinalAction FinalAction `yaml:"final_action"`
	Agent       Agent       `yaml:"agent"`
}

// TaskSource describe de dónde salen las tareas.
type TaskSource struct {
	Tipo      string            `yaml:"tipo"`      // stdin | file | api | queue
	Ruta      string            `yaml:"ruta"`      // file
	Dir       string            `yaml:"dir"`       // queue
	URL       string            `yaml:"url"`       // api
	Metodo    string            `yaml:"metodo"`    // api: GET | POST
	Campo     string            `yaml:"campo"`     // campo del JSON/payload con la tarea
	Intervalo time.Duration     `yaml:"intervalo"` // api/queue: sondeo
	Headers   map[string]string `yaml:"headers"`   // api
	Cuerpo    string            `yaml:"cuerpo"`    // api: cuerpo para POST
}

// Anchor es el validador determinista (Capa A). Puede ser un comando único o
// una lista de comprobaciones; todas deben pasar.
type Anchor struct {
	Tipo          string        `yaml:"tipo"` // command | none
	Comando       string        `yaml:"comando"`
	Argumentos    []string      `yaml:"argumentos"`
	Timeout       time.Duration `yaml:"timeout"`
	EsperarExit   int           `yaml:"esperar_exit"`
	EsperarSalida string        `yaml:"esperar_salida"` // expresión regular opcional
	Checks        []Check       `yaml:"checks"`
}

// Check es una comprobación adicional del ancla.
type Check struct {
	Nombre        string        `yaml:"nombre"`
	Comando       string        `yaml:"comando"`
	Argumentos    []string      `yaml:"argumentos"`
	Timeout       time.Duration `yaml:"timeout"`
	EsperarExit   int           `yaml:"esperar_exit"`
	EsperarSalida string        `yaml:"esperar_salida"`
}

// Sandbox describe el entorno de ejecución aislada (Capa C).
type Sandbox struct {
	Tipo               string        `yaml:"tipo"`    // none | chroot | cgroups
	Raiz               string        `yaml:"raiz"`    // chroot: directorio raíz
	Usuario            string        `yaml:"usuario"` // "uid:gid" opcional
	MemoriaMB          int           `yaml:"memoria_mb"`
	CPUSegundos        int           `yaml:"cpu_segundos"`
	Procesos           int           `yaml:"procesos"`
	ArchivosAbiertos   int           `yaml:"archivos_abiertos"`
	TamanoMaxArchivoMB int           `yaml:"tamano_max_archivo_mb"`
	AislarRed          bool          `yaml:"aislar_red"`
	Cgroups            string        `yaml:"cgroups"` // auto | on | off
	CgroupRaiz         string        `yaml:"cgroup_raiz"`
	Timeout            time.Duration `yaml:"timeout"`
	ConservarEfimero   bool          `yaml:"conservar_efimero"`
	SalidaMaxKB        int           `yaml:"salida_max_kb"`
}

// LLM describe el motor de razonamiento (Capa B).
type LLM struct {
	Proveedor      string        `yaml:"proveedor"` // openai | anthropic | gemini
	Modelo         string        `yaml:"modelo"`
	APIKey         string        `yaml:"api_key"`
	BaseURL        string        `yaml:"base_url"`
	MaxTokens      int           `yaml:"max_tokens"`
	Temperature    float64       `yaml:"temperature"`
	Timeout        time.Duration `yaml:"timeout"`
	MaxIntentos    int           `yaml:"max_intentos"`
	BackoffInicial time.Duration `yaml:"backoff_inicial"`
	BackoffMax     time.Duration `yaml:"backoff_max"`
}

// Plantilla es un prompt con variables {{nombre}}.
type Plantilla struct {
	Sistema string `yaml:"sistema"`
	Usuario string `yaml:"usuario"`
}

// Prompts agrupa las tres plantillas del flujo.
type Prompts struct {
	Analyze Plantilla `yaml:"analyze"`
	Plan    Plantilla `yaml:"plan"`
	Execute Plantilla `yaml:"execute"`
}

// FinalAction es lo que se ejecuta cuando el ancla da PASS.
type FinalAction struct {
	Tipo          string   `yaml:"tipo"` // none | command | api | git_commit
	Comando       string   `yaml:"comando"`
	Argumentos    []string `yaml:"argumentos"`
	URL           string   `yaml:"url"`
	Metodo        string   `yaml:"metodo"`
	MensajeCommit string   `yaml:"mensaje_commit"`
}

// Escalar es la acción de escalado cuando se agotan los reintentos.
type Escalar struct {
	Tipo    string `yaml:"tipo"` // none | command
	Comando string `yaml:"comando"`
}

// Agent agrupa los parámetros del bucle principal.
type Agent struct {
	MaxReintentos        int           `yaml:"max_reintentos"`
	ProfundidadSubtareas int           `yaml:"profundidad_subtareas"`
	MaxTareas            int           `yaml:"max_tareas"`
	WorkspaceDir         string        `yaml:"workspace_dir"`
	LogFile              string        `yaml:"log_file"`
	LogNivel             string        `yaml:"log_level"`
	LogConsola           bool          `yaml:"log_consola"`
	LogMaxMB             int           `yaml:"log_max_mb"`
	LogBackups           int           `yaml:"log_backups"`
	ApagadoTimeout       time.Duration `yaml:"graceful_shutdown_timeout"`
	Escalar              Escalar       `yaml:"escalar"`
}

// ---------------------------------------------------------------------------
// Valores por defecto
// ---------------------------------------------------------------------------

// Defecto devuelve una configuración completa y usable sin archivo YAML.
func Defecto() Config {
	return Config{
		TaskSource: TaskSource{
			Tipo:      "stdin",
			Metodo:    "GET",
			Campo:     "tarea",
			Intervalo: 30 * time.Second,
		},
		Anchor: Anchor{
			Tipo:    "none",
			Timeout: 120 * time.Second,
		},
		Sandbox: Sandbox{
			Tipo:               "none",
			Cgroups:            "auto",
			CgroupRaiz:         "/sys/fs/cgroup",
			MemoriaMB:          512,
			CPUSegundos:        60,
			Procesos:           128,
			ArchivosAbiertos:   256,
			TamanoMaxArchivoMB: 64,
			Timeout:            300 * time.Second,
			SalidaMaxKB:        256,
		},
		LLM: LLM{
			Proveedor:      "openai",
			Modelo:         "gpt-4o-mini",
			BaseURL:        "https://api.openai.com/v1",
			MaxTokens:      2048,
			Temperature:    0.2,
			Timeout:        90 * time.Second,
			MaxIntentos:    3,
			BackoffInicial: time.Second,
			BackoffMax:     30 * time.Second,
		},
		Prompts: Prompts{
			Analyze: PlantillaBaseAnalyze,
			Plan:    PlantillaBasePlan,
			Execute: PlantillaBaseExecute,
		},
		FinalAction: FinalAction{
			Tipo:          "none",
			Metodo:        "POST",
			MensajeCommit: "agente: {{tarea}}",
		},
		Agent: Agent{
			MaxReintentos:        3,
			ProfundidadSubtareas: 1,
			WorkspaceDir:         "./workspace",
			LogNivel:             "info",
			LogConsola:           true,
			LogMaxMB:             5,
			LogBackups:           3,
			ApagadoTimeout:       15 * time.Second,
			Escalar:              Escalar{Tipo: "none"},
		},
	}
}

// ---------------------------------------------------------------------------
// Carga
// ---------------------------------------------------------------------------

// Cargar lee el YAML, aplica los valores por defecto que falten, superpone las
// variables de entorno y valida el resultado, exigiendo la clave del LLM.
func Cargar(ruta string) (Config, error) {
	return cargar(ruta, true)
}

// CargarSinClave hace lo mismo pero sin exigir llm.api_key.
//
// Existe para los modos que no llaman al LLM (-validar-config, -aislamiento):
// ahí no tener clave es legítimo. Lo que NO es aceptable es que un archivo
// inválido se sustituya en silencio por la configuración por defecto y se
// informe "configuración válida": eso ocultaría precisamente el error que se
// está intentando comprobar.
func CargarSinClave(ruta string) (Config, error) {
	return cargar(ruta, false)
}

func cargar(ruta string, exigirClave bool) (Config, error) {
	cfg := Defecto()

	if ruta != "" {
		datos, err := os.ReadFile(ruta)
		if err != nil {
			return cfg, fmt.Errorf("no se pudo leer la configuración %q: %w", ruta, err)
		}
		mapa, err := ParseYAML(datos)
		if err != nil {
			return cfg, fmt.Errorf("YAML inválido en %q: %w", ruta, err)
		}
		if err := Decodificar(mapa, &cfg); err != nil {
			return cfg, fmt.Errorf("configuración inválida en %q: %w", ruta, err)
		}
	}

	// Variables de entorno (ganan sobre el YAML) y compatibilidad con las
	// variables estándar OPENAI_*.
	if err := AplicarEntorno(&cfg); err != nil {
		return cfg, err
	}
	if v := os.Getenv("OPENAI_API_KEY"); cfg.LLM.APIKey == "" && v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("OPENAI_BASE_URL"); v != "" && os.Getenv("STARLIGHT_LLM_BASE_URL") == "" && cfg.LLM.BaseURL == Defecto().LLM.BaseURL {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("OPENAI_MODEL"); v != "" && os.Getenv("STARLIGHT_LLM_MODELO") == "" && cfg.LLM.Modelo == Defecto().LLM.Modelo {
		cfg.LLM.Modelo = v
	}

	if err := cfg.validar(exigirClave); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// Validar comprueba coherencia y rellena lo que se pueda deducir, exigiendo la
// clave del LLM.
func (c *Config) Validar() error { return c.validar(true) }

// ValidarSinClave es igual pero tolera la ausencia de llm.api_key.
func (c *Config) ValidarSinClave() error { return c.validar(false) }

// validar es el cuerpo común de Validar y ValidarSinClave.
func (c *Config) validar(exigirClave bool) error {
	c.TaskSource.Tipo = normalizar(c.TaskSource.Tipo)
	c.Anchor.Tipo = normalizar(c.Anchor.Tipo)
	c.Sandbox.Tipo = normalizar(c.Sandbox.Tipo)
	c.LLM.Proveedor = normalizar(c.LLM.Proveedor)
	c.FinalAction.Tipo = normalizar(c.FinalAction.Tipo)
	c.Agent.LogNivel = normalizar(c.Agent.LogNivel)

	switch c.TaskSource.Tipo {
	case "stdin", "file", "api", "queue":
	default:
		return fmt.Errorf("task_source.tipo desconocido: %q (usa stdin, file, api o queue)", c.TaskSource.Tipo)
	}
	if c.TaskSource.Tipo == "file" && c.TaskSource.Ruta == "" {
		return fmt.Errorf("task_source.tipo=file requiere 'ruta'")
	}
	if c.TaskSource.Tipo == "queue" && c.TaskSource.Dir == "" {
		return fmt.Errorf("task_source.tipo=queue requiere 'dir'")
	}
	if c.TaskSource.Tipo == "api" && c.TaskSource.URL == "" {
		return fmt.Errorf("task_source.tipo=api requiere 'url'")
	}

	switch c.Anchor.Tipo {
	case "none":
	case "command":
		if c.Anchor.Comando == "" {
			return fmt.Errorf("anchor.tipo=command requiere 'comando'")
		}
	default:
		return fmt.Errorf("anchor.tipo desconocido: %q (usa command o none)", c.Anchor.Tipo)
	}

	switch c.Sandbox.Tipo {
	case "none", "chroot", "cgroups":
	default:
		return fmt.Errorf("sandbox.tipo desconocido: %q (usa none, chroot o cgroups)", c.Sandbox.Tipo)
	}
	if c.Sandbox.Tipo == "chroot" && c.Sandbox.Raiz == "" {
		return fmt.Errorf("sandbox.tipo=chroot requiere 'raiz' (directorio raíz del chroot)")
	}
	switch c.Sandbox.Cgroups {
	case "", "auto", "on", "off":
	default:
		return fmt.Errorf("sandbox.cgroups desconocido: %q (usa auto, on u off)", c.Sandbox.Cgroups)
	}
	if c.Sandbox.Usuario != "" {
		if _, _, err := ParsearUsuario(c.Sandbox.Usuario); err != nil {
			return err
		}
	}

	switch c.LLM.Proveedor {
	case "openai", "anthropic", "gemini":
	default:
		return fmt.Errorf("llm.proveedor desconocido: %q (usa openai, anthropic o gemini)", c.LLM.Proveedor)
	}
	if c.LLM.Modelo == "" {
		return fmt.Errorf("llm.modelo no puede estar vacío")
	}
	if exigirClave && c.LLM.APIKey == "" {
		return fmt.Errorf("falta la clave del LLM: define llm.api_key en el YAML o STARLIGHT_LLM_API_KEY (o OPENAI_API_KEY)")
	}
	if c.LLM.MaxIntentos < 1 {
		return fmt.Errorf("llm.max_intentos debe ser >= 1")
	}
	if c.LLM.BackoffInicial <= 0 {
		return fmt.Errorf("llm.backoff_inicial debe ser mayor que cero")
	}
	if c.LLM.BackoffMax < c.LLM.BackoffInicial {
		return fmt.Errorf("llm.backoff_max no puede ser menor que llm.backoff_inicial")
	}

	switch c.FinalAction.Tipo {
	case "none", "command":
	case "api":
		if c.FinalAction.URL == "" {
			return fmt.Errorf("final_action.tipo=api requiere 'url'")
		}
	case "git_commit":
	default:
		return fmt.Errorf("final_action.tipo desconocido: %q (usa none, command, api o git_commit)", c.FinalAction.Tipo)
	}

	if c.Agent.MaxReintentos < 0 {
		return fmt.Errorf("agent.max_reintentos no puede ser negativo")
	}
	if c.Agent.WorkspaceDir == "" {
		c.Agent.WorkspaceDir = Defecto().Agent.WorkspaceDir
	}
	switch c.Agent.LogNivel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("agent.log_level desconocido: %q (usa debug, info, warn o error)", c.Agent.LogNivel)
	}
	if c.Agent.LogBackups < 0 {
		return fmt.Errorf("agent.log_backups no puede ser negativo")
	}
	if c.Agent.Escalar.Tipo == "" {
		c.Agent.Escalar.Tipo = "none"
	}
	if c.Agent.Escalar.Tipo != "none" && c.Agent.Escalar.Tipo != "command" {
		return fmt.Errorf("agent.escalar.tipo desconocido: %q (usa none o command)", c.Agent.Escalar.Tipo)
	}
	return nil
}

func normalizar(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ParsearUsuario interpreta "uid:gid" (también acepta sólo "uid").
func ParsearUsuario(s string) (uid, gid int, err error) {
	partes := strings.Split(strings.TrimSpace(s), ":")
	if len(partes) > 2 || partes[0] == "" {
		return 0, 0, fmt.Errorf("sandbox.usuario debe tener el formato uid:gid, se recibió %q", s)
	}
	uid, err = parseEntero(partes[0])
	if err != nil {
		return 0, 0, fmt.Errorf("sandbox.usuario: uid inválido en %q: %w", s, err)
	}
	gid = uid
	if len(partes) == 2 {
		gid, err = parseEntero(partes[1])
		if err != nil {
			return 0, 0, fmt.Errorf("sandbox.usuario: gid inválido en %q: %w", s, err)
		}
	}
	return uid, gid, nil
}

func parseEntero(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n)
	if err != nil {
		return 0, fmt.Errorf("%q no es un número", s)
	}
	return n, nil
}
