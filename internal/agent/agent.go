// Package agent implementa el bucle principal del agente, que orquesta las tres
// capas:
//
//	Capa A (anchor)  valida de forma determinista, sin razonamiento
//	Capa B (llm)     analiza, planifica y propone acciones
//	Capa C (sandbox) ejecuta las acciones aisladas
//
// El bucle es el del documento de arquitectura:
//
//	[1] leer tarea             [6] LLM: generar la acción
//	[2] extraer contexto       [7] ejecutar en el sandbox
//	[3] LLM: analizar          [8] validar con el ancla
//	[4] LLM: planificar        [9] PASS -> acción final / FAIL -> reintentar
//	[5] dividir en subtareas       y al agotar los intentos -> escalar
//
// El agente no decide por su cuenta cuándo ha terminado: sólo el ancla puede
// declarar PASS.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/anchor"
	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/llm"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/plantilla"
	"github.com/madkoding/starlight/internal/sandbox"
	"github.com/madkoding/starlight/internal/task"
)

// Agente orquesta las tres capas.
type Agente struct {
	cfg     config.Config
	log     *logx.Logger
	motor   *llm.Cliente
	sandbox *sandbox.Sandbox
	fuente  task.Fuente
	http    *http.Client

	// Observador, si no es nil, recibe el resultado de cada tarea al terminar.
	// Es el punto de integración para métricas o para quien embebe el agente, y
	// también lo que usan las pruebas para inspeccionar el veredicto sin leer
	// el registro.
	Observador func(ResultadoTarea)
}

// NuevoAgente construye el agente con todas sus dependencias ya construidas.
func NuevoAgente(cfg config.Config, log *logx.Logger, motor *llm.Cliente, caja *sandbox.Sandbox, fuente task.Fuente) *Agente {
	if log == nil {
		log = logx.Global()
	}
	return &Agente{
		cfg:     cfg,
		log:     log,
		motor:   motor,
		sandbox: caja,
		fuente:  fuente,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
}

// --- Resultados de cada fase -------------------------------------------------

// Analisis es la salida estructurada de la fase [3].
type Analisis struct {
	Comprensible      bool     `json:"comprensible"`
	Resumen           string   `json:"resumen"`
	CriteriosExito    []string `json:"criterios_exito"`
	Riesgos           []string `json:"riesgos"`
	NecesitaSubtareas bool     `json:"necesita_subtareas"`
}

// Plan es la salida estructurada de la fase [4].
type Plan struct {
	Pasos             []Paso   `json:"plan"`
	Subtareas         []string `json:"subtareas"`
	ResultadoEsperado string   `json:"resultado_esperado"`
}

// Paso es un paso del plan.
type Paso struct {
	Numero  int    `json:"paso"`
	Accion  string `json:"accion"`
	Comando string `json:"comando"`
}

// Accion es lo que devuelve la fase [6].
type Accion struct {
	Razonamiento string    `json:"razonamiento"`
	Acciones     []Comando `json:"acciones"`
	AccionFinal  Comando   `json:"accion_final"`
}

// Comando es una acción ejecutable.
type Comando struct {
	Tipo        string `json:"tipo"`
	Descripcion string `json:"descripcion"`
	Comando     string `json:"comando"`
}

// ResultadoTarea es el veredicto final de una tarea.
type ResultadoTarea struct {
	Tarea       string            `json:"tarea"`
	PASS        bool              `json:"pass"`
	Intentos    int               `json:"intentos"`
	Subtareas   int               `json:"subtareas"`
	DuracionMS  int64             `json:"duracion_ms"`
	Validacion  *anchor.Resultado `json:"validacion,omitempty"`
	AccionFinal string            `json:"accion_final,omitempty"`
	Motivo      string            `json:"motivo"`
}

// --- Bucle principal --------------------------------------------------------

// Ejecutar procesa tareas de la fuente hasta que se agota (io.EOF) o el
// contexto se cancela (apagado ordenado).
func (a *Agente) Ejecutar(ctx context.Context) error {
	if strings.EqualFold(a.cfg.Anchor.Tipo, "none") || a.cfg.Anchor.Tipo == "" {
		// Sin ancla no existe la autoridad que declara PASS, así que todas las
		// tareas acabarían escaladas tras gastar intentos contra el LLM. Fallar
		// ahora, con la solución concreta, es más barato y más honesto.
		return errors.New("anchor.tipo=none: este agente sólo declara una tarea como completada " +
			"cuando un validador determinista da PASS, y no hay ninguno configurado.\n" +
			"   Configura un ancla real, por ejemplo:\n" +
			"     anchor:\n       tipo: command\n       comando: make\n       argumentos: [test]\n" +
			"   Si sólo quieres probar el bucle sin validar nada, hazlo explícito:\n" +
			"     anchor:\n       tipo: command\n       comando: true\n" +
			"   Comprueba la configuración con: starlight -config <archivo> -validar-config")
	}

	a.log.Info("agente iniciado",
		"fuente", a.fuente.Descripcion(),
		"proveedor", a.cfg.LLM.Proveedor,
		"modelo", a.cfg.LLM.Modelo,
		"ancla", a.cfg.Anchor.Tipo,
		"sandbox", strings.Join(modosATexto(a.sandbox.Aislamiento()), ","),
		"workspace", a.cfg.Agent.WorkspaceDir,
		"max_reintentos", a.cfg.Agent.MaxReintentos)

	defer a.fuente.Cerrar()

	total := 0
	fallidas := 0
	motivos := []string{}

	for {
		select {
		case <-ctx.Done():
			a.log.Info("apagado solicitado, terminando", "tareas_procesadas", total)
			return nil
		default:
		}

		if a.cfg.Agent.MaxTareas > 0 && total >= a.cfg.Agent.MaxTareas {
			a.log.Info("se alcanzó agent.max_tareas", "tareas", total)
			break
		}

		tarea, err := a.fuente.Siguiente(ctx)
		if errors.Is(err, io.EOF) {
			a.log.Info("no hay más tareas", "tareas_procesadas", total)
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			a.log.Error("no se pudo obtener la tarea", "error", err)
			fallidas++
			continue
		}
		if strings.TrimSpace(tarea.Descripcion) == "" {
			continue
		}

		total++
		resultado := a.procesarTarea(ctx, tarea, 0)
		if resultado.PASS {
			a.log.Info("tarea completada",
				"tarea", recortar(tarea.Descripcion, 120),
				"intentos", resultado.Intentos,
				"duracion_ms", resultado.DuracionMS,
				"accion_final", resultado.AccionFinal)
		} else {
			fallidas++
			motivos = append(motivos, fmt.Sprintf("%q: %s", recortar(tarea.Descripcion, 80), resultado.Motivo))
			a.log.Error("tarea fallida",
				"tarea", recortar(tarea.Descripcion, 120),
				"intentos", resultado.Intentos,
				"motivo", resultado.Motivo)
		}
	}

	a.log.Info("agente terminado", "tareas", total, "fallidas", fallidas)
	if fallidas > 0 {
		// Se incluye el motivo de cada fallo: quien lea el error (o la salida de
		// cron) debe poder actuar sin ir a buscar el registro.
		return fmt.Errorf("%d de %d tareas fallaron: %s", fallidas, total, strings.Join(motivos, " | "))
	}
	return nil
}

// procesarTarea ejecuta el bucle de 9 pasos para una tarea y notifica el
// resultado al observador (una única vez, sea cual sea la ruta de salida).
func (a *Agente) procesarTarea(ctx context.Context, t task.Tarea, profundidad int) ResultadoTarea {
	r := a.bucle(ctx, t, profundidad)
	if a.Observador != nil {
		a.Observador(r)
	}
	return r
}

// bucle es el bucle de 9 pasos propiamente dicho.
func (a *Agente) bucle(ctx context.Context, t task.Tarea, profundidad int) ResultadoTarea {
	inicio := time.Now()
	res := ResultadoTarea{Tarea: t.Descripcion, Intentos: 0}

	prefijo := strings.Repeat("  ", profundidad)
	a.log.Info(prefijo+"tarea recibida", "origen", t.Origen, "profundidad", profundidad, "descripcion", recortar(t.Descripcion, 200))

	// [2] Extraer contexto y criterios de éxito: se construye el enunciado de
	// las reglas de validación que se le mostrarán al LLM, para que sepa contra
	// qué se le va a medir.
	reglas := a.describirReglas()

	// [3] Análisis.
	analisis := a.faseAnalisis(ctx, t, reglas, profundidad)
	if !analisis.Comprensible {
		res.Motivo = "la tarea se declaró no comprensible: " + strings.Join(analisis.Riesgos, "; ")
		res.DuracionMS = time.Since(inicio).Milliseconds()
		a.log.Warn(prefijo+"tarea no comprensible, se descarta", "motivo", res.Motivo)
		return res
	}

	// [4] Plan.
	plan := a.fasePlan(ctx, t, analisis, profundidad)

	// [5] División en subtareas: cada una vuelve a entrar por el mismo flujo, un
	// nivel más abajo. Se respeta el límite para no entrar en recursión infinita.
	if analisis.NecesitaSubtareas && len(plan.Subtareas) > 0 {
		if profundidad >= a.cfg.Agent.ProfundidadSubtareas {
			a.log.Warn(prefijo+"la división en subtareas alcanzó el límite configurado; se continúa como tarea única",
				"profundidad", profundidad, "limite", a.cfg.Agent.ProfundidadSubtareas,
				"subtareas", len(plan.Subtareas))
		} else {
			a.log.Info(prefijo+"dividiendo en subtareas", "cantidad", len(plan.Subtareas))
			exitosas := 0
			for _, sub := range plan.Subtareas {
				if ctx.Err() != nil {
					res.Motivo = "cancelado durante las subtareas"
					res.DuracionMS = time.Since(inicio).Milliseconds()
					return res
				}
				if strings.TrimSpace(sub) == "" {
					continue
				}
				res.Subtareas++
				subResultado := a.procesarTarea(ctx, task.Tarea{
					Descripcion: sub,
					Origen:      t.Origen + " (subtarea)",
				}, profundidad+1)
				if subResultado.PASS {
					exitosas++
				}
			}
			res.PASS = exitosas == res.Subtareas && res.Subtareas > 0
			res.Intentos = 1
			res.DuracionMS = time.Since(inicio).Milliseconds()
			if res.PASS {
				res.Motivo = fmt.Sprintf("%d subtareas completadas", exitosas)
			} else {
				res.Motivo = fmt.Sprintf("sólo %d de %d subtareas pasaron la validación", exitosas, res.Subtareas)
			}
			return res
		}
	}

	// [6]-[9] Ciclo de ejecución y validación.
	intentosFallidos := []string{}

	for intento := 1; intento <= a.cfg.Agent.MaxReintentos+1; intento++ {
		res.Intentos = intento

		// [6] El LLM propone la acción concreta.
		accion, err := a.faseAccion(ctx, t, plan, intentosFallidos, intento, prefijo)
		if err != nil {
			res.Motivo = "no se pudo obtener la acción del LLM: " + err.Error()
			res.DuracionMS = time.Since(inicio).Milliseconds()
			a.escalar(ctx, res, prefijo)
			return res
		}

		// [7] Ejecutar en el sandbox.
		salidaEjecucion, errEjecucion := a.ejecutarAcciones(ctx, accion.Acciones, prefijo)

		// [8] Validar con el ancla, siempre. El ancla se ejecuta aunque la
		// ejecución haya fallado, porque su trabajo es describir el estado real
		// del sistema, no opinar sobre lo que hizo el agente.
		validacion := anchor.Nuevo(a.cfg.Anchor, a.cfg.Agent.WorkspaceDir, a.sandbox).Validar(ctx)
		res.Validacion = &validacion

		if validacion.PASS && errEjecucion == nil {
			// [9] PASS de la validación: ahora la acción final, que también forma
			// parte del contrato (commit, publicar, notificar). Si la acción
			// final falla, la tarea NO está completa: informar "completada"
			// mientras el commit o la publicación falló sería exactamente el
			// tipo de éxito falso que esta arquitectura existe para evitar.
			accionFinal, errFinal := a.ejecutarAccionFinal(ctx, accion.AccionFinal, prefijo)
			res.AccionFinal = accionFinal
			if errFinal != nil {
				detalle := fmt.Sprintf("la validación pasó pero la acción final falló: %v\nSalida: %s", errFinal, accionFinal)
				intentosFallidos = append(intentosFallidos, detalle)
				a.log.Error(prefijo+"la acción final falló", "intento", intento, "accion_final", accionFinal, "error", errFinal)
				if intento > a.cfg.Agent.MaxReintentos {
					res.Motivo = detalle
					res.DuracionMS = time.Since(inicio).Milliseconds()
					a.escalar(ctx, res, prefijo)
					return res
				}
				continue
			}
			res.PASS = true
			res.Motivo = validacion.Motivo
			res.DuracionMS = time.Since(inicio).Milliseconds()
			a.log.Info(prefijo+"validación superada", "intento", intento, "accion_final", accionFinal)
			return res
		}

		// FAIL: se acumulan los registros para que el LLM corrija con datos.
		detalle := a.resumirFallo(accion, salidaEjecucion, validacion, errEjecucion)
		intentosFallidos = append(intentosFallidos, detalle)
		a.log.Warn(prefijo+"intento fallido",
			"intento", intento, "max_intentos", a.cfg.Agent.MaxReintentos+1,
			"validacion", validacion.Motivo)

		if intento > a.cfg.Agent.MaxReintentos {
			break
		}
	}

	// Se agotaron los intentos: escalar.
	res.Motivo = fmt.Sprintf("se agotaron los %d intentos sin superar la validación", a.cfg.Agent.MaxReintentos+1)
	res.DuracionMS = time.Since(inicio).Milliseconds()
	a.escalar(ctx, res, prefijo)
	return res
}

// --- Fases ------------------------------------------------------------------

func (a *Agente) faseAnalisis(ctx context.Context, t task.Tarea, reglas string, profundidad int) Analisis {
	vars := a.variablesBase(t)
	vars["reglas"] = reglas
	vars["intento"] = "1"
	vars["max_intentos"] = fmt.Sprint(a.cfg.Agent.MaxReintentos + 1)

	texto, err := a.pedir(ctx, a.cfg.Prompts.Analyze, vars, "analyze")
	if err != nil {
		a.log.Error("fase de análisis fallida", "error", err)
		return Analisis{Comprensible: false, Riesgos: []string{err.Error()}}
	}

	var analisis Analisis
	if err := llm.DecodificarJSON(texto, &analisis); err != nil {
		a.log.Error("el análisis no tiene el formato esperado", "error", err, "respuesta", recortar(texto, 300))
		return Analisis{Comprensible: false, Riesgos: []string{"el análisis del LLM no era JSON válido: " + err.Error()}}
	}
	if analisis.Resumen == "" {
		analisis.Resumen = recortar(t.Descripcion, 200)
	}
	a.log.Info("análisis completado", "resumen", recortar(analisis.Resumen, 160), "criterios", len(analisis.CriteriosExito))
	return analisis
}

func (a *Agente) fasePlan(ctx context.Context, t task.Tarea, analisis Analisis, profundidad int) Plan {
	vars := a.variablesBase(t)
	var sb strings.Builder
	if analisis.Resumen != "" {
		fmt.Fprintf(&sb, "- Resumen: %s\n", analisis.Resumen)
	}
	for _, c := range analisis.CriteriosExito {
		fmt.Fprintf(&sb, "- Criterio de éxito: %s\n", c)
	}
	for _, r := range analisis.Riesgos {
		fmt.Fprintf(&sb, "- Riesgo: %s\n", r)
	}
	vars["analisis"] = sb.String()

	texto, err := a.pedir(ctx, a.cfg.Prompts.Plan, vars, "plan")
	if err != nil {
		a.log.Error("fase de planificación fallida", "error", err)
		return Plan{}
	}
	var plan Plan
	if err := llm.DecodificarJSON(texto, &plan); err != nil {
		a.log.Error("el plan no tiene el formato esperado", "error", err, "respuesta", recortar(texto, 300))
		return Plan{}
	}
	a.log.Info("plan generado", "pasos", len(plan.Pasos), "subtareas", len(plan.Subtareas))
	return plan
}

func (a *Agente) faseAccion(ctx context.Context, t task.Tarea, plan Plan, fallos []string, intento int, prefijo string) (Accion, error) {
	vars := a.variablesBase(t)
	vars["plan"] = describirPlan(plan)
	vars["historial"] = plantilla.Historial(fallos)
	vars["intento"] = fmt.Sprint(intento)
	vars["max_intentos"] = fmt.Sprint(a.cfg.Agent.MaxReintentos + 1)

	texto, err := a.pedir(ctx, a.cfg.Prompts.Execute, vars, "execute")
	if err != nil {
		return Accion{}, err
	}
	var accion Accion
	if err := llm.DecodificarJSON(texto, &accion); err != nil {
		return Accion{}, err
	}
	if len(accion.Acciones) == 0 {
		return Accion{}, errors.New("el LLM no propuso ninguna acción")
	}
	a.log.Info(prefijo+"acción propuesta", "razonamiento", recortar(accion.Razonamiento, 200), "acciones", len(accion.Acciones))
	return accion, nil
}

// pedir renderiza el prompt y llama al LLM, registrando las variables que
// faltaron (huecos sin rellenar en una plantilla del usuario).
func (a *Agente) pedir(ctx context.Context, p config.Plantilla, vars map[string]string, fase string) (string, error) {
	sistema, faltanS := plantilla.Render(p.Sistema, vars)
	usuario, faltanU := plantilla.Render(p.Usuario, vars)
	if len(faltanS)+len(faltanU) > 0 {
		faltantes := append(append([]string{}, faltanS...), faltanU...)
		a.log.Warn("la plantilla tiene variables sin valor", "fase", fase, "variables", strings.Join(faltantes, ","))
	}

	mensajes := []llm.Mensaje{}
	if strings.TrimSpace(sistema) != "" {
		mensajes = append(mensajes, llm.Mensaje{Rol: "system", Contenido: sistema})
	}
	mensajes = append(mensajes, llm.Mensaje{Rol: "user", Contenido: usuario})

	inicio := time.Now()
	texto, err := a.motor.Completar(ctx, mensajes)
	if err != nil {
		return "", err
	}
	a.log.Debug("respuesta del LLM", "fase", fase, "ms", time.Since(inicio).Milliseconds(), "bytes", len(texto))
	return texto, nil
}

// variablesBase son las variables disponibles en todas las plantillas.
func (a *Agente) variablesBase(t task.Tarea) map[string]string {
	vars := map[string]string{
		"tarea":        t.Descripcion,
		"workspace":    a.cfg.Agent.WorkspaceDir,
		"origen":       t.Origen,
		"intento":      "1",
		"max_intentos": fmt.Sprint(a.cfg.Agent.MaxReintentos + 1),
		"reglas":       a.describirReglas(),
		"analisis":     "",
		"plan":         "",
		"historial":    "",
		"modelo":       a.cfg.LLM.Modelo,
		"proveedor":    a.cfg.LLM.Proveedor,
	}
	for k, v := range t.Contexto {
		vars["contexto_"+k] = v
	}
	return vars
}

// describirReglas convierte el ancla en texto para el prompt: el LLM debe saber
// exactamente contra qué se le va a medir.
func (a *Agente) describirReglas() string {
	switch strings.ToLower(a.cfg.Anchor.Tipo) {
	case "command":
		var sb strings.Builder
		fmt.Fprintf(&sb, "- Se ejecutará: %s %s\n", a.cfg.Anchor.Comando, strings.Join(a.cfg.Anchor.Argumentos, " "))
		fmt.Fprintf(&sb, "- Debe terminar con código de salida %d.\n", a.cfg.Anchor.EsperarExit)
		if a.cfg.Anchor.EsperarSalida != "" {
			fmt.Fprintf(&sb, "- Su salida debe coincidir con la expresión regular: %s\n", a.cfg.Anchor.EsperarSalida)
		}
		for _, c := range a.cfg.Anchor.Checks {
			fmt.Fprintf(&sb, "- Además: %s %s (código esperado %d)\n", c.Comando, strings.Join(c.Argumentos, " "), c.EsperarExit)
		}
		return sb.String()
	default:
		return "- No hay validación determinista configurada (anchor.tipo=none). El agente no declarará PASS por sí mismo: revisa la configuración."
	}
}

func describirPlan(p Plan) string {
	if len(p.Pasos) == 0 {
		return "(sin plan)"
	}
	var sb strings.Builder
	for _, paso := range p.Pasos {
		fmt.Fprintf(&sb, "%d. %s", paso.Numero, paso.Accion)
		if paso.Comando != "" {
			fmt.Fprintf(&sb, "  ->  %s", paso.Comando)
		}
		sb.WriteString("\n")
	}
	if p.ResultadoEsperado != "" {
		fmt.Fprintf(&sb, "Resultado esperado: %s\n", p.ResultadoEsperado)
	}
	return sb.String()
}

// --- Ejecución --------------------------------------------------------------

// ejecutarAcciones corre cada acción en el sandbox y devuelve la salida
// combinada de todas.
func (a *Agente) ejecutarAcciones(ctx context.Context, acciones []Comando, prefijo string) (string, error) {
	var sb strings.Builder
	var ultimo error

	for i, accion := range acciones {
		if strings.TrimSpace(accion.Comando) == "" {
			a.log.Debug(prefijo+"acción sin comando (descriptiva)", "descripcion", accion.Descripcion)
			continue
		}
		a.log.Info(prefijo+"ejecutando en sandbox", "n", i+1, "comando", accion.Comando, "descripcion", recortar(accion.Descripcion, 120))

		salida, truncado, exit, err := a.sandbox.Ejecutar(ctx, execx.Peticion{
			Comando: "/bin/sh",
			Args:    []string{"-c", accion.Comando},
			Timeout: a.cfg.Sandbox.Timeout,
		})

		fmt.Fprintf(&sb, "$ %s\n", accion.Comando)
		if salida != "" {
			sb.WriteString(salida)
			if !strings.HasSuffix(salida, "\n") {
				sb.WriteString("\n")
			}
		}
		if truncado {
			sb.WriteString("[salida truncada por el límite del sandbox]\n")
		}
		fmt.Fprintf(&sb, "[exit=%d]\n", exit)

		if err != nil {
			ultimo = fmt.Errorf("la acción %d (%s) no pudo ejecutarse: %w", i+1, accion.Comando, err)
		}
	}
	return sb.String(), ultimo
}

// resumirFallo arma el bloque que se le pasa al LLM en el siguiente intento.
func (a *Agente) resumirFallo(accion Accion, ejecucion string, validacion anchor.Resultado, errEjecucion error) string {
	var sb strings.Builder
	sb.WriteString("Acciones propuestas:\n")
	for _, c := range accion.Acciones {
		fmt.Fprintf(&sb, "- %s\n", c.Comando)
	}
	if ejecucion != "" {
		sb.WriteString("\nSalida de la ejecución en el sandbox:\n")
		sb.WriteString(recortar(ejecucion, 3000))
		sb.WriteString("\n")
	}
	if errEjecucion != nil {
		fmt.Fprintf(&sb, "\nError de ejecución: %s\n", errEjecucion)
	}
	sb.WriteString("\nResultado de la validación determinista (JSON):\n")
	sb.WriteString(validacion.JSON())
	sb.WriteString("\n")
	return sb.String()
}

// --- Acción final y escalado ------------------------------------------------

// ejecutarAccionFinal ejecuta la acción prevista sólo tras un PASS. Devuelve una
// descripción legible y un error si la acción no se pudo completar (incluido un
// código de salida distinto de cero, que es un fallo aunque no haya error de
// ejecución).
func (a *Agente) ejecutarAccionFinal(ctx context.Context, c Comando, prefijo string) (string, error) {
	final := a.cfg.FinalAction

	switch strings.ToLower(final.Tipo) {
	case "", "none":
		return "none", nil

	case "command":
		comando := final.Comando
		args := final.Argumentos
		if strings.TrimSpace(c.Comando) != "" {
			// El LLM puede proponer el comando final concreto.
			comando, args = "/bin/sh", []string{"-c", c.Comando}
		}
		if strings.TrimSpace(comando) == "" {
			a.log.Warn(prefijo + "final_action.tipo=command sin comando: no se hace nada")
			return "none", nil
		}
		a.log.Info(prefijo+"ejecutando la acción final", "comando", comando)
		salida, _, exit, err := a.sandbox.Ejecutar(ctx, execx.Peticion{Comando: comando, Args: args, Timeout: a.cfg.Sandbox.Timeout})
		descripcion := fmt.Sprintf("command exit=%d salida=%s", exit, recortar(salida, 300))
		if err != nil {
			a.log.Error(prefijo+"la acción final falló", "error", err, "salida", recortar(salida, 500))
			return descripcion, fmt.Errorf("la acción final no pudo ejecutarse: %w", err)
		}
		if exit != 0 {
			return descripcion, fmt.Errorf("la acción final terminó con código %d", exit)
		}
		return descripcion, nil

	case "api":
		cuerpo := map[string]any{
			"tarea":   c.Descripcion,
			"comando": c.Comando,
			"estado":  "pass",
		}
		datos, _ := json.Marshal(cuerpo)
		metodo := final.Metodo
		if metodo == "" {
			metodo = http.MethodPost
		}
		req, err := http.NewRequestWithContext(ctx, metodo, final.URL, bytes.NewReader(datos))
		if err != nil {
			a.log.Error(prefijo+"petición final inválida", "error", err)
			return "error: " + err.Error(), fmt.Errorf("la acción final (API) tiene una URL inválida: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := a.http.Do(req)
		if err != nil {
			a.log.Error(prefijo+"la notificación final falló", "error", err)
			return "error: " + err.Error(), fmt.Errorf("la acción final (API) falló: %w", err)
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		descripcion := fmt.Sprintf("api %s %s -> HTTP %d", metodo, final.URL, resp.StatusCode)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return descripcion, fmt.Errorf("la acción final (API) devolvió HTTP %d", resp.StatusCode)
		}
		return descripcion, nil

	case "git_commit":
		mensaje, _ := plantilla.Render(final.MensajeCommit, map[string]string{"tarea": c.Descripcion})
		if strings.TrimSpace(mensaje) == "" {
			mensaje = "agente: cambios validados"
		}
		secuencia := []string{
			"git add -A",
			fmt.Sprintf("git commit -m %s", comillaShell(mensaje)),
		}
		var salidas []string
		for _, cmd := range secuencia {
			salida, _, exit, err := a.sandbox.Ejecutar(ctx, execx.Peticion{
				Comando: "/bin/sh", Args: []string{"-c", cmd}, Timeout: a.cfg.Sandbox.Timeout,
			})
			salidas = append(salidas, fmt.Sprintf("%s -> exit=%d %s", cmd, exit, recortar(salida, 200)))
			if err != nil {
				a.log.Error(prefijo+"git_commit falló", "comando", cmd, "error", err)
				return "git_commit: " + strings.Join(salidas, " | "), fmt.Errorf("git_commit: %s falló: %w", cmd, err)
			}
			if exit != 0 {
				return "git_commit: " + strings.Join(salidas, " | "), fmt.Errorf("git_commit: %s terminó con código %d", cmd, exit)
			}
		}
		return "git_commit: " + strings.Join(salidas, " | "), nil

	default:
		return "none", fmt.Errorf("final_action.tipo desconocido: %q", final.Tipo)
	}
}

// escalar activa la acción de escalado configurada cuando la tarea no pasa.
func (a *Agente) escalar(ctx context.Context, res ResultadoTarea, prefijo string) {
	if a.cfg.Agent.Escalar.Tipo != "command" || strings.TrimSpace(a.cfg.Agent.Escalar.Comando) == "" {
		return
	}
	a.log.Warn(prefijo+"escalando tras agotar los intentos", "comando", a.cfg.Agent.Escalar.Comando)
	salida, _, exit, err := a.sandbox.Ejecutar(ctx, execx.Peticion{
		Comando: "/bin/sh",
		Args:    []string{"-c", a.cfg.Agent.Escalar.Comando},
		Timeout: a.cfg.Sandbox.Timeout,
	})
	if err != nil {
		a.log.Error(prefijo+"la acción de escalado falló", "error", err)
		return
	}
	a.log.Info(prefijo+"escalado ejecutado", "exit", exit, "salida", recortar(salida, 300))
}

// comillaShell cita un texto para pasarlo a sh -c.
func comillaShell(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func modosATexto(modos []sandbox.Modo) []string {
	salida := make([]string, 0, len(modos))
	for _, m := range modos {
		salida = append(salida, string(m))
	}
	return salida
}

func recortar(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
