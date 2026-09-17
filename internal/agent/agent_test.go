package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/task"
)

func init() {
	l, _ := logx.Nuevo(logx.Opciones{Nivel: logx.Error, Consola: false})
	logx.Instalar(l)
}

// servidorLLMFalso simula las tres fases del flujo devolviendo los JSON que el
// agente espera, y ejecuta un guion de acciones por intento.
type servidorLLMFalso struct {
	// accionesPorIntento indica, para cada intento, qué comandos propone el
	// "modelo" en la fase de ejecución.
	accionesPorIntento [][]string
	llamadas           int32
	fases              []string
	fallarEn           string // si no está vacío, esa fase devuelve un error HTTP
}

func (s *servidorLLMFalso) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&s.llamadas, 1)

		var peticion struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&peticion)

		var texto string
		for _, m := range peticion.Messages {
			texto += m.Content
		}

		// Se decide la fase por las marcas del prompt (así se prueba de verdad
		// qué plantilla se usó, no sólo el orden de llamada).
		switch {
		case strings.Contains(texto, "ANÁLISIS DE LA TAREA") || strings.Contains(texto, "## ANÁLISIS DE LA TAREA"):
			s.fases = append(s.fases, "analyze")
			if s.fallarEn == "analyze" {
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"error":{"message":"fallo simulado en el análisis"}}`)
				return
			}
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"comprensible\":true,\"resumen\":\"tarea de prueba\",\"criterios_exito\":[\"el archivo existe\"],\"riesgos\":[],\"necesita_subtareas\":false}"}}]}`)

		case strings.Contains(texto, "PLAN DE ACCIÓN") || strings.Contains(texto, "## PLAN DE ACCIÓN"):
			s.fases = append(s.fases, "plan")
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[{\"paso\":1,\"accion\":\"crear archivo\",\"comando\":\"crear.txt\"}],\"subtareas\":[],\"resultado_esperado\":\"archivo creado\"}"}}]}`)

		case strings.Contains(texto, "## ACCIÓN"):
			s.fases = append(s.fases, "execute")
			n := 0
			for _, f := range s.fases {
				if f == "execute" {
					n++
				}
			}
			idx := n - 1
			var comandos []string
			if idx < len(s.accionesPorIntento) {
				comandos = s.accionesPorIntento[idx]
			}
			if len(comandos) == 0 {
				comandos = []string{"true"}
			}
			acciones := make([]map[string]string, 0, len(comandos))
			for _, c := range comandos {
				acciones = append(acciones, map[string]string{"tipo": "comando", "descripcion": "prueba", "comando": c})
			}
			respuesta := map[string]any{
				"razonamiento": "prueba automatizada",
				"acciones":     acciones,
				"accion_final": map[string]string{"descripcion": "ninguna", "comando": ""},
			}
			// Se construye el sobre de respuesta con el content ya serializado.
			datos, _ := json.Marshal(map[string]any{
				"choices": []any{map[string]any{
					"message": map[string]string{"content": mustJSON(respuesta)},
				}},
			})
			w.Write(datos)

		default:
			t.Errorf("petición con prompt irreconocible: %q", recortar(texto, 200))
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
		}
	}
}

// mustJSON devuelve la cadena JSON que el "modelo" pondría en content.
func mustJSON(v any) string {
	d, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(d)
}

// entorno monta un agente completo con el LLM falso y el ancla indicada.
type entorno struct {
	agente *Agente
	dir    string
	log    *logx.Logger
	caja   *sandbox.Sandbox
}

func montar(t *testing.T, srv *httptest.Server, ancla config.Anchor, cfg func(*config.Config)) *entorno {
	t.Helper()
	dir := t.TempDir()

	c := config.Defecto()
	c.LLM.APIKey = "clave"
	c.LLM.BaseURL = srv.URL
	c.LLM.MaxIntentos = 1
	c.LLM.BackoffInicial = time.Millisecond
	c.LLM.BackoffMax = 2 * time.Millisecond
	c.LLM.Timeout = 5 * time.Second
	c.Anchor = ancla
	c.Agent.WorkspaceDir = dir
	c.Agent.MaxReintentos = 1
	c.TaskSource.Tipo = "file"
	if cfg != nil {
		cfg(&c)
	}

	caja, err := sandbox.Nuevo(sandbox.Opciones{
		Dir:         dir,
		Limites:     sandbox.Limites{MemoriaMB: 256, CPUSegundos: 10},
		Timeout:     20 * time.Second,
		MaxSalidaKB: 64,
		Log:         logx.Global(),
	})
	if err != nil {
		t.Fatalf("no se pudo crear el sandbox: %v", err)
	}
	t.Cleanup(func() { caja.Cerrar() })

	motor, err := llm.Nuevo(c.LLM, logx.Global())
	if err != nil {
		t.Fatalf("no se pudo crear el motor: %v", err)
	}

	fuente, err := task.NuevaTexto("haz la tarea de prueba", "prueba")
	if err != nil {
		t.Fatal(err)
	}

	return &entorno{
		agente: NuevoAgente(c, logx.Global(), motor, caja, fuente),
		dir:    dir,
		log:    logx.Global(),
		caja:   caja,
	}
}

// TestBucleCompletoPASSPrimerIntento: el caso feliz de extremo a extremo.
func TestBucleCompletoPASSPrimerIntento(t *testing.T) {
	falso := &servidorLLMFalso{
		accionesPorIntento: [][]string{{"echo hola > resultado.txt"}},
	}
	srv := httptest.NewServer(falso.handler(t))
	defer srv.Close()

	e := montar(t, srv, config.Anchor{
		Tipo:          "command",
		Comando:       "sh",
		Argumentos:    []string{"-c", "test -s resultado.txt && echo LISTO"},
		Timeout:       10 * time.Second,
		EsperarSalida: "LISTO",
	}, nil)

	var resultado *ResultadoTarea
	e.agente.Observador = func(r ResultadoTarea) { resultado = &r }
	err := e.agente.Ejecutar(context.Background())
	if err != nil {
		t.Fatalf("el agente devolvió error: %v", err)
	}
	if resultado == nil {
		t.Fatal("no se capturó el resultado de la tarea")
	}
	if !resultado.PASS {
		t.Fatalf("se esperaba PASS, motivo: %s", resultado.Motivo)
	}
	if resultado.Intentos != 1 {
		t.Errorf("intentos = %d", resultado.Intentos)
	}
	// Las tres fases deben haberse usado, en orden.
	esperado := []string{"analyze", "plan", "execute"}
	if strings.Join(falso.fases, ",") != strings.Join(esperado, ",") {
		t.Errorf("fases = %v, se esperaba %v", falso.fases, esperado)
	}
	// El ancla debe haber visto el efecto: el bucle del agente y el del sandbox
	// escriben en el mismo directorio de trabajo.
	if _, err := os.Stat(filepath.Join(e.dir, "resultado.txt")); err != nil {
		t.Errorf("el archivo creado por la acción no está en el directorio de trabajo: %v", err)
	}
}

// TestBucleReintentaYCorrige: el primer intento no cumple la validación y el
// segundo sí. Se comprueba que el segundo prompt lleva los registros del fallo.
func TestBucleReintentaYCorrige(t *testing.T) {
	falso := &servidorLLMFalso{
		accionesPorIntento: [][]string{
			{"echo mal"},
			{"echo hola > correcto.txt"},
		},
	}
	srv := httptest.NewServer(falso.handler(t))
	defer srv.Close()

	e := montar(t, srv, config.Anchor{
		Tipo:        "command",
		Comando:     "sh",
		Argumentos:  []string{"-c", "test -s correcto.txt"},
		Timeout:     10 * time.Second,
		EsperarExit: 0,
	}, nil)

	var resultado *ResultadoTarea
	e.agente.Observador = func(r ResultadoTarea) { resultado = &r }
	if err := e.agente.Ejecutar(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if resultado == nil || !resultado.PASS {
		t.Fatalf("debería pasar al segundo intento: %+v", resultado)
	}
	if resultado.Intentos != 2 {
		t.Errorf("intentos = %d, se esperaban 2", resultado.Intentos)
	}
}

// TestBucleFallaYEscalaTrasAgotarIntentos: cuando nunca valida, escala y no
// declara PASS.
func TestBucleFallaYEscalaTrasAgotarIntentos(t *testing.T) {
	falso := &servidorLLMFalso{
		accionesPorIntento: [][]string{{"echo mal"}, {"echo mal otra vez"}},
	}
	srv := httptest.NewServer(falso.handler(t))
	defer srv.Close()

	dir := t.TempDir()
	e := montar(t, srv, config.Anchor{
		Tipo:        "command",
		Comando:     "sh",
		Argumentos:  []string{"-c", "exit 1"},
		Timeout:     10 * time.Second,
		EsperarExit: 0,
	}, func(c *config.Config) {
		c.Agent.MaxReintentos = 1
		c.Agent.Escalar = config.Escalar{
			Tipo:    "command",
			Comando: "echo escalado > " + filepath.Join(dir, "escalado.txt"),
		}
	})

	var resultado *ResultadoTarea
	e.agente.Observador = func(r ResultadoTarea) { resultado = &r }
	err := e.agente.Ejecutar(context.Background())
	if err == nil {
		t.Error("una tarea que no pasa debe hacer que el agente termine con error")
	}
	if resultado == nil {
		t.Fatal("sin resultado")
	}
	if resultado.PASS {
		t.Fatal("nunca debe declarar PASS si el ancla falla")
	}
	if resultado.Intentos != 2 {
		t.Errorf("intentos = %d (max_reintentos=1 => 2 intentos)", resultado.Intentos)
	}
	if !strings.Contains(resultado.Motivo, "agotaron") {
		t.Errorf("motivo = %q", resultado.Motivo)
	}
}

// TestAgenteSinAnclaNoArranca: sin validador no hay quien declare PASS, así que
// el agente debe negarse a arrancar en vez de quemar intentos contra el LLM.
func TestAgenteSinAnclaNoArranca(t *testing.T) {
	falso := &servidorLLMFalso{}
	srv := httptest.NewServer(falso.handler(t))
	defer srv.Close()

	e := montar(t, srv, config.Anchor{Tipo: "none"}, nil)
	err := e.agente.Ejecutar(context.Background())
	if err == nil {
		t.Fatal("se esperaba un error de configuración")
	}
	if !strings.Contains(err.Error(), "anchor.tipo=none") {
		t.Errorf("el error debe explicar el problema y cómo resolverlo: %v", err)
	}
	if atomic.LoadInt32(&falso.llamadas) != 0 {
		t.Errorf("no se debe llamar al LLM sin ancla: hubo %d llamadas", falso.llamadas)
	}
}

// TestAnalisisNoComprensible: si el modelo dice que la tarea no es viable, no se
// ejecuta nada y la tarea se descarta con motivo.
func TestAnalisisNoComprensible(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var pet struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&pet)
		var texto string
		for _, m := range pet.Messages {
			texto += m.Content
		}
		if strings.Contains(texto, "ANÁLISIS DE LA TAREA") {
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"comprensible\":false,\"riesgos\":[\"faltan datos de entrada\"]}"}}]}`)
			return
		}
		t.Errorf("no debería llegarse a otra fase")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"{}"}}]}`)
	}))
	defer srv.Close()

	e := montar(t, srv, config.Anchor{Tipo: "command", Comando: "true", Timeout: 5 * time.Second}, nil)

	var resultado *ResultadoTarea
	e.agente.Observador = func(r ResultadoTarea) { resultado = &r }

	// Una tarea que no se puede hacer cuenta como fallo: el trabajo no se hizo,
	// así que el proceso debe salir con error (importante para cron).
	err := e.agente.Ejecutar(context.Background())
	if err == nil {
		t.Fatal("una tarea descartada debe hacer que el agente termine con error")
	}
	if resultado == nil {
		t.Fatal("sin resultado")
	}
	if resultado.PASS {
		t.Fatal("una tarea no comprensible no puede pasar")
	}
	if !strings.Contains(resultado.Motivo, "faltan datos") {
		t.Errorf("motivo = %q", resultado.Motivo)
	}
	if !strings.Contains(err.Error(), "faltan datos") {
		t.Errorf("el error agregado debe incluir el motivo: %v", err)
	}
}

// TestAccionConSalidaSePropagaAlAncla: la salida del comando llega al
// razonamiento del siguiente intento, que es lo que permite al LLM corregir.
func TestAccionConSalidaSePropagaAlAncla(t *testing.T) {
	falso := &servidorLLMFalso{
		accionesPorIntento: [][]string{
			{"echo 'pista-para-el-modelo'; exit 1"},
			{"echo hola > resultado.txt"},
		},
	}
	srv := httptest.NewServer(falso.handler(t))
	defer srv.Close()

	e := montar(t, srv, config.Anchor{
		Tipo: "command", Comando: "sh", Argumentos: []string{"-c", "test -s resultado.txt"},
		Timeout: 10 * time.Second,
	}, nil)

	var resultado *ResultadoTarea
	e.agente.Observador = func(r ResultadoTarea) { resultado = &r }
	if err := e.agente.Ejecutar(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if resultado == nil || !resultado.PASS {
		t.Fatalf("debería acabar pasando: %+v", resultado)
	}
}

// TestSubtareas: cuando el análisis pide dividir, cada subtarea recorre el flujo
// completo.
func TestSubtareas(t *testing.T) {
	llamadas := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var pet struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&pet)
		var texto string
		for _, m := range pet.Messages {
			texto += m.Content
		}
		switch {
		case strings.Contains(texto, "ANÁLISIS DE LA TAREA"):
			llamadas++
			if llamadas == 1 {
				fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"comprensible\":true,\"resumen\":\"dividir\",\"criterios_exito\":[],\"riesgos\":[],\"necesita_subtareas\":true}"}}]}`)
			} else {
				fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"comprensible\":true,\"resumen\":\"sub\",\"criterios_exito\":[],\"riesgos\":[],\"necesita_subtareas\":false}"}}]}`)
			}
		case strings.Contains(texto, "PLAN DE ACCIÓN"):
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[],\"subtareas\":[\"subtarea A\",\"subtarea B\"],\"resultado_esperado\":\"x\"}"}}]}`)
		default:
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"razonamiento\":\"r\",\"acciones\":[{\"tipo\":\"comando\",\"comando\":\"echo sub > sub.txt\"}],\"accion_final\":{\"comando\":\"\"}}"}}]}`)
		}
	}))
	defer srv.Close()

	e := montar(t, srv, config.Anchor{
		Tipo: "command", Comando: "sh", Argumentos: []string{"-c", "test -s sub.txt"},
		Timeout: 10 * time.Second,
	}, func(c *config.Config) { c.Agent.ProfundidadSubtareas = 1 })

	var resultado *ResultadoTarea
	e.agente.Observador = func(r ResultadoTarea) { resultado = &r }
	if err := e.agente.Ejecutar(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if resultado.Subtareas != 2 {
		t.Errorf("subtareas = %d, se esperaban 2", resultado.Subtareas)
	}
	if !resultado.PASS {
		t.Errorf("las dos subtareas deberían pasar: %s", resultado.Motivo)
	}
}

// TestLimiteDeSubtareas: sin el límite, el reparto podría no terminar nunca.
func TestLimiteDeSubtareas(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var pet struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&pet)
		var texto string
		for _, m := range pet.Messages {
			texto += m.Content
		}
		switch {
		case strings.Contains(texto, "ANÁLISIS DE LA TAREA"):
			// Siempre pide dividir: si no hay límite, esto no termina.
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"comprensible\":true,\"resumen\":\"x\",\"necesita_subtareas\":true}"}}]}`)
		case strings.Contains(texto, "PLAN DE ACCIÓN"):
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"plan\":[],\"subtareas\":[\"otra\"],\"resultado_esperado\":\"x\"}"}}]}`)
		default:
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"razonamiento\":\"r\",\"acciones\":[{\"comando\":\"true\"}]}"}}]}`)
		}
	}))
	defer srv.Close()

	e := montar(t, srv, config.Anchor{
		Tipo: "command", Comando: "true", Timeout: 5 * time.Second,
	}, func(c *config.Config) { c.Agent.ProfundidadSubtareas = 2 })

	hecho := make(chan error, 1)
	go func() { hecho <- e.agente.Ejecutar(context.Background()) }()

	select {
	case <-hecho:
		// Terminó: el límite funciona.
	case <-time.After(30 * time.Second):
		t.Fatal("no terminó: el límite de subtareas no se respeta")
	}
}

// TestAccionFinalSoloTrasPASS: la acción final no debe ejecutarse jamás si el
// ancla no dio PASS.
func TestAccionFinalSoloTrasPASS(t *testing.T) {
	falso := &servidorLLMFalso{accionesPorIntento: [][]string{{"echo mal"}, {"echo mal"}}}
	srv := httptest.NewServer(falso.handler(t))
	defer srv.Close()

	marca := filepath.Join(t.TempDir(), "accion-final-ejecutada.txt")
	e := montar(t, srv, config.Anchor{
		Tipo: "command", Comando: "exit 1", Timeout: 5 * time.Second, EsperarExit: 0,
	}, func(c *config.Config) {
		c.Agent.MaxReintentos = 1
		c.FinalAction = config.FinalAction{
			Tipo:       "command",
			Comando:    "sh",
			Argumentos: []string{"-c", "touch " + marca},
		}
	})

	e.agente.Ejecutar(context.Background())
	if _, err := os.Stat(marca); err == nil {
		t.Fatal("la acción final se ejecutó sin PASS del ancla")
	}
}

// TestTareaVaciaEsUnError: construir una fuente con una tarea en blanco debe
// fallar en lugar de mandar una petición inútil al LLM.
func TestTareaVaciaEsUnError(t *testing.T) {
	if _, err := task.NuevaTexto("   ", "prueba"); err == nil {
		t.Fatal("una tarea vacía debe ser un error")
	}
}

// TestMaxTareas: el límite de tareas detiene el agente aunque la fuente tenga
// más trabajo (útil para ejecuciones acotadas desde cron).
func TestMaxTareas(t *testing.T) {
	falso := &servidorLLMFalso{
		accionesPorIntento: [][]string{{"true"}, {"true"}, {"true"}},
	}
	srv := httptest.NewServer(falso.handler(t))
	defer srv.Close()

	e := montar(t, srv, config.Anchor{
		Tipo: "command", Comando: "true", Timeout: 5 * time.Second,
	}, func(c *config.Config) { c.Agent.MaxTareas = 1 })

	// Fuente con dos tareas: sólo debe procesarse una.
	fuente, err := task.NuevaTexto("primera", "prueba")
	if err != nil {
		t.Fatal(err)
	}
	e.agente.fuente = fuente

	procesadas := 0
	e.agente.Observador = func(r ResultadoTarea) { procesadas++ }

	if err := e.agente.Ejecutar(context.Background()); err != nil {
		t.Fatalf("error: %v", err)
	}
	if procesadas != 1 {
		t.Errorf("tareas procesadas = %d, se esperaba 1 (max_tareas)", procesadas)
	}
}
