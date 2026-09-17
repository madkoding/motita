// Command starlight es un agente autónomo de 3 capas para máquinas i386.
//
// Arquitectura (ver README.md):
//
//	Capa A — EL ANCLA: validador determinista, sin razonamiento. Compilado
//	         nativamente, ejecuta comprobaciones reales y devuelve PASS/FAIL con
//	         registros JSON.
//	Capa B — EL MOTOR DE RAZONAMIENTO: cliente LLM ligero (OpenAI, Anthropic o
//	         Gemini), sin SDK, con reintentos y backoff exponencial.
//	Capa C — EL SANDBOX: ejecución aislada sin Docker (directorio efímero,
//	         setrlimit, cgroups v1 opcionales, chroot opcional).
//
// Todo es configurable sin recompilar: ver configs/agent.yaml.example y
// configs/casos/*.yaml para tres casos de uso completos.
//
// Uso:
//
//	starlight -config configs/agent.yaml.example
//	starlight -config configs/casos/1-desarrollo.yaml -tarea "arregla el test X"
//	starlight -validar-config configs/agent.yaml.example
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/madkoding/starlight/internal/agent"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/task"
)

// version se inyecta con -ldflags "-X main.version=v1.0.0".
var version = "dev"

func main() {
	// El sandbox se re-ejecuta a sí mismo para aplicar los límites en el hijo.
	// Este caso se atiende antes de parsear banderas: la línea de comandos del
	// hijo es propia del sandbox, no del usuario.
	if sandbox.EsEjecucionHijo(os.Args[1:]) {
		if err := sandbox.EjecutarComoHijo(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "starlight[sandbox]: %v\n", err)
			os.Exit(126)
		}
		return
	}

	var (
		rutaConfig    = flag.String("config", "", "ruta del archivo YAML de configuración")
		rutaTarea     = flag.String("tarea", "", "procesa una única tarea (ignora la fuente configurada)")
		archivoTarea  = flag.String("archivo-tarea", "", "procesa la tarea contenida en un archivo")
		validarConfig = flag.Bool("validar-config", false, "valida la configuración y sale (no llama al LLM)")
		verVersion    = flag.Bool("version", false, "muestra la versión y sale")
		mostrarAisl   = flag.Bool("aislamiento", false, "muestra el aislamiento del sandbox disponible y sale")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Uso: starlight [opciones]\n\nOpciones:\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nVariables de entorno: STARLIGHT_* (ver README.md; también acepta OPENAI_API_KEY).\n")
	}
	flag.Parse()

	if *verVersion {
		fmt.Printf("starlight %s (%s/%s)\n", version, goos(), goarch())
		return
	}

	// Los modos que no llaman al LLM no exigen la clave, pero un archivo
	// inválido es un error SIEMPRE: informar "configuración válida" tras
	// sustituir el archivo por los valores por defecto ocultaría justo el fallo
	// que se quiere detectar.
	cfg, err := config.Cargar(*rutaConfig)
	if err != nil && (*validarConfig || *mostrarAisl) {
		cfg, err = config.CargarSinClave(*rutaConfig)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(2)
	}

	nivel, err := logx.ParsearNivel(cfg.Agent.LogNivel)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(2)
	}

	// Registro estructurado con rotación.
	log, err := logx.Nuevo(logx.Opciones{
		Ruta:    cfg.Agent.LogFile,
		Nivel:   nivel,
		Consola: cfg.Agent.LogConsola,
		MaxMB:   cfg.Agent.LogMaxMB,
		Backups: cfg.Agent.LogBackups,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(2)
	}
	logx.Instalar(log)
	defer log.Cerrar()

	// Sandbox (Capa C).
	caja, err := sandbox.Nuevo(opcionesSandbox(cfg, log))
	if err != nil {
		log.Error("no se pudo preparar el sandbox", "error", err)
		fmt.Fprintf(os.Stderr, "❌ no se pudo preparar el sandbox: %v\n", err)
		os.Exit(1)
	}
	defer caja.Cerrar()

	if *mostrarAisl {
		fmt.Printf("aislamiento aplicado: %v\n", caja.Aislamiento())
		if sin := caja.SinAplicar(); len(sin) > 0 {
			fmt.Printf("solicitado y no aplicado:\n")
			for _, s := range sin {
				fmt.Printf("  - %s\n", s)
			}
		}
		return
	}

	if *validarConfig {
		fmt.Printf("✅ configuración válida: %s\n", describirConfig(cfg))
		fmt.Printf("   aislamiento del sandbox: %v\n", caja.Aislamiento())
		return
	}

	// Motor de razonamiento (Capa B).
	motor, err := llm.Nuevo(cfg.LLM, log)
	if err != nil {
		log.Error("no se pudo preparar el motor de razonamiento", "error", err)
		fmt.Fprintf(os.Stderr, "❌ %v\n", err)
		os.Exit(2)
	}

	// Fuente de tareas (una única tarea tiene prioridad sobre la configurada).
	fuente := construirFuente(cfg, *rutaTarea, *archivoTarea, log)

	ag := agent.NuevoAgente(cfg, log, motor, caja, fuente)

	// Apagado ordenado: el primer SIGINT/SIGTERM cancela el contexto y da tiempo
	// a terminar la tarea en curso; el segundo sale de inmediato.
	ctx, cancelar := context.WithCancel(context.Background())
	defer cancelar()

	senal := make(chan os.Signal, 2)
	signal.Notify(senal, syscall.SIGINT, syscall.SIGTERM)

	listo := make(chan struct{})
	go func() {
		select {
		case s := <-senal:
			log.Warn("señal recibida: terminando de forma ordenada", "senal", s.String(),
				"plazo", cfg.Agent.ApagadoTimeout.String())
			cancelar()
		case <-listo:
			return
		}
		select {
		case s := <-senal:
			log.Error("segunda señal recibida: salida inmediata", "senal", s.String())
			os.Exit(130)
		case <-time.After(cfg.Agent.ApagadoTimeout):
			log.Error("el apagado ordenado excedió el plazo: salida forzada",
				"plazo", cfg.Agent.ApagadoTimeout.String())
			os.Exit(130)
		}
	}()

	empezado := time.Now()
	errEjecucion := ag.Ejecutar(ctx)
	close(listo)

	if errEjecucion != nil {
		log.Error("el agente terminó con errores", "error", errEjecucion, "duracion", time.Since(empezado).String())
		fmt.Fprintf(os.Stderr, "❌ %v (ver el registro en %s)\n", errEjecucion, rutaRegistro(cfg))
		os.Exit(1)
	}
	log.Info("ejecución completada", "duracion", time.Since(empezado).String())
}

// opcionesSandbox traduce la configuración a las opciones del sandbox.
func opcionesSandbox(cfg config.Config, log *logx.Logger) sandbox.Opciones {
	op := sandbox.Opciones{
		Dir: cfg.Agent.WorkspaceDir,
		Limites: sandbox.Limites{
			MemoriaMB:        cfg.Sandbox.MemoriaMB,
			CPUSegundos:      cfg.Sandbox.CPUSegundos,
			Procesos:         cfg.Sandbox.Procesos,
			ArchivosAbiertos: cfg.Sandbox.ArchivosAbiertos,
			TamanoArchivoMB:  cfg.Sandbox.TamanoMaxArchivoMB,
		},
		CgroupRaiz:  cfg.Sandbox.CgroupRaiz,
		Timeout:     cfg.Sandbox.Timeout,
		MaxSalidaKB: cfg.Sandbox.SalidaMaxKB,
		Conservar:   cfg.Sandbox.ConservarEfimero,
		Log:         log,
	}

	switch cfg.Sandbox.Tipo {
	case "none":
		// Sin aislamiento de sistema de archivos: sólo directorio efímero.
		op.Limites = sandbox.Limites{}
		op.UsarCgroups = false
		op.UsarChroot = false
	case "chroot":
		op.UsarChroot = true
		op.Raiz = cfg.Sandbox.Raiz
		op.UsarCgroups = deseadoCgroups(cfg)
	case "cgroups":
		op.UsarCgroups = deseadoCgroups(cfg)
	default:
		op.UsarCgroups = deseadoCgroups(cfg)
	}

	if uid, gid, err := config.ParsearUsuario(cfg.Sandbox.Usuario); err == nil && cfg.Sandbox.Usuario != "" {
		op.Uid, op.Gid, op.DropPrivs = uid, gid, true
	}
	op.Limites.SinRed = cfg.Sandbox.AislarRed
	return op
}

// deseadoCgroups decide si se intenta usar cgroups v1.
func deseadoCgroups(cfg config.Config) bool {
	switch cfg.Sandbox.Cgroups {
	case "on":
		return true
	case "off":
		return false
	default: // auto: se intenta y se degrada con un aviso si no hay
		return true
	}
}

// construirFuente resuelve de dónde salen las tareas.
func construirFuente(cfg config.Config, tareaDirecta, archivo string, log *logx.Logger) task.Fuente {
	switch {
	case tareaDirecta != "":
		f, err := task.NuevaTexto(tareaDirecta, "linea-de-comandos")
		if err != nil {
			log.Error("no se pudo construir la tarea de la línea de comandos", "error", err)
			os.Exit(2)
		}
		return f
	case archivo != "":
		f, err := task.NuevaArchivo(archivo)
		if err != nil {
			log.Error("no se pudo leer el archivo de tarea", "error", err, "archivo", archivo)
			os.Exit(2)
		}
		return f
	default:
		f, err := task.Nueva(cfg.TaskSource, log)
		if err != nil {
			log.Error("no se pudo construir la fuente de tareas", "error", err)
			fmt.Fprintf(os.Stderr, "❌ %v\n", err)
			os.Exit(2)
		}
		return f
	}
}

func describirConfig(cfg config.Config) string {
	registro := cfg.Agent.LogFile
	if registro == "" {
		registro = "(sólo consola)"
	}
	return fmt.Sprintf("task_source=%s llm=%s/%s anchor=%s sandbox=%s workspace=%s log=%s",
		cfg.TaskSource.Tipo, cfg.LLM.Proveedor, cfg.LLM.Modelo, cfg.Anchor.Tipo,
		cfg.Sandbox.Tipo, cfg.Agent.WorkspaceDir, registro)
}

func rutaRegistro(cfg config.Config) string {
	if cfg.Agent.LogFile == "" {
		return "stderr"
	}
	return cfg.Agent.LogFile
}
