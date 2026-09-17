package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Superposición de variables de entorno.
//
// Convención: STARLIGHT_<BLOQUE>_<CAMPO>. Ganan sobre el YAML, que es lo que se
// espera al inyectar secretos en un contenedor o en un servicio systemd.
// ---------------------------------------------------------------------------

// AplicarEntorno superpone las variables STARLIGHT_* sobre la configuración.
func AplicarEntorno(c *Config) error {
	var err error

	// --- task_source ---
	c.TaskSource.Tipo = leerTexto("STARLIGHT_TASK_SOURCE_TIPO", c.TaskSource.Tipo)
	c.TaskSource.Ruta = leerTexto("STARLIGHT_TASK_SOURCE_RUTA", c.TaskSource.Ruta)
	c.TaskSource.Dir = leerTexto("STARLIGHT_TASK_SOURCE_DIR", c.TaskSource.Dir)
	c.TaskSource.URL = leerTexto("STARLIGHT_TASK_SOURCE_URL", c.TaskSource.URL)
	c.TaskSource.Metodo = leerTexto("STARLIGHT_TASK_SOURCE_METODO", c.TaskSource.Metodo)
	c.TaskSource.Campo = leerTexto("STARLIGHT_TASK_SOURCE_CAMPO", c.TaskSource.Campo)
	c.TaskSource.Cuerpo = leerTexto("STARLIGHT_TASK_SOURCE_CUERPO", c.TaskSource.Cuerpo)
	if c.TaskSource.Intervalo, err = leerDuracion("STARLIGHT_TASK_SOURCE_INTERVALO", c.TaskSource.Intervalo); err != nil {
		return err
	}

	// --- anchor ---
	c.Anchor.Tipo = leerTexto("STARLIGHT_ANCHOR_TIPO", c.Anchor.Tipo)
	c.Anchor.Comando = leerTexto("STARLIGHT_ANCHOR_COMANDO", c.Anchor.Comando)
	if c.Anchor.Timeout, err = leerDuracion("STARLIGHT_ANCHOR_TIMEOUT", c.Anchor.Timeout); err != nil {
		return err
	}
	if c.Anchor.EsperarExit, err = leerEntero("STARLIGHT_ANCHOR_ESPERAR_EXIT", c.Anchor.EsperarExit); err != nil {
		return err
	}
	c.Anchor.EsperarSalida = leerTexto("STARLIGHT_ANCHOR_ESPERAR_SALIDA", c.Anchor.EsperarSalida)

	// --- sandbox ---
	c.Sandbox.Tipo = leerTexto("STARLIGHT_SANDBOX_TIPO", c.Sandbox.Tipo)
	c.Sandbox.Raiz = leerTexto("STARLIGHT_SANDBOX_RAIZ", c.Sandbox.Raiz)
	c.Sandbox.Usuario = leerTexto("STARLIGHT_SANDBOX_USUARIO", c.Sandbox.Usuario)
	c.Sandbox.Cgroups = leerTexto("STARLIGHT_SANDBOX_CGROUPS", c.Sandbox.Cgroups)
	c.Sandbox.CgroupRaiz = leerTexto("STARLIGHT_SANDBOX_CGROUP_RAIZ", c.Sandbox.CgroupRaiz)
	if c.Sandbox.MemoriaMB, err = leerEntero("STARLIGHT_SANDBOX_MEMORIA_MB", c.Sandbox.MemoriaMB); err != nil {
		return err
	}
	if c.Sandbox.CPUSegundos, err = leerEntero("STARLIGHT_SANDBOX_CPU_SEGUNDOS", c.Sandbox.CPUSegundos); err != nil {
		return err
	}
	if c.Sandbox.Procesos, err = leerEntero("STARLIGHT_SANDBOX_PROCESOS", c.Sandbox.Procesos); err != nil {
		return err
	}
	if c.Sandbox.Timeout, err = leerDuracion("STARLIGHT_SANDBOX_TIMEOUT", c.Sandbox.Timeout); err != nil {
		return err
	}
	if c.Sandbox.AislarRed, err = leerBool("STARLIGHT_SANDBOX_AISLAR_RED", c.Sandbox.AislarRed); err != nil {
		return err
	}

	// --- llm ---
	c.LLM.Proveedor = leerTexto("STARLIGHT_LLM_PROVEEDOR", c.LLM.Proveedor)
	c.LLM.Modelo = leerTexto("STARLIGHT_LLM_MODELO", c.LLM.Modelo)
	c.LLM.APIKey = leerTexto("STARLIGHT_LLM_API_KEY", c.LLM.APIKey)
	c.LLM.BaseURL = leerTexto("STARLIGHT_LLM_BASE_URL", c.LLM.BaseURL)
	if c.LLM.MaxTokens, err = leerEntero("STARLIGHT_LLM_MAX_TOKENS", c.LLM.MaxTokens); err != nil {
		return err
	}
	if v := os.Getenv("STARLIGHT_LLM_TEMPERATURE"); v != "" {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return fmt.Errorf("STARLIGHT_LLM_TEMPERATURE: %q no es un número", v)
		}
		c.LLM.Temperature = f
	}
	if c.LLM.Timeout, err = leerDuracion("STARLIGHT_LLM_TIMEOUT", c.LLM.Timeout); err != nil {
		return err
	}
	if c.LLM.MaxIntentos, err = leerEntero("STARLIGHT_LLM_MAX_INTENTOS", c.LLM.MaxIntentos); err != nil {
		return err
	}
	if c.LLM.BackoffInicial, err = leerDuracion("STARLIGHT_LLM_BACKOFF_INICIAL", c.LLM.BackoffInicial); err != nil {
		return err
	}
	if c.LLM.BackoffMax, err = leerDuracion("STARLIGHT_LLM_BACKOFF_MAX", c.LLM.BackoffMax); err != nil {
		return err
	}

	// --- final_action ---
	c.FinalAction.Tipo = leerTexto("STARLIGHT_FINAL_ACTION_TIPO", c.FinalAction.Tipo)
	c.FinalAction.Comando = leerTexto("STARLIGHT_FINAL_ACTION_COMANDO", c.FinalAction.Comando)
	c.FinalAction.URL = leerTexto("STARLIGHT_FINAL_ACTION_URL", c.FinalAction.URL)
	c.FinalAction.MensajeCommit = leerTexto("STARLIGHT_FINAL_ACTION_MENSAJE_COMMIT", c.FinalAction.MensajeCommit)

	// --- agent ---
	if c.Agent.MaxReintentos, err = leerEntero("STARLIGHT_AGENT_MAX_REINTENTOS", c.Agent.MaxReintentos); err != nil {
		return err
	}
	if c.Agent.ProfundidadSubtareas, err = leerEntero("STARLIGHT_AGENT_PROFUNDIDAD_SUBTAREAS", c.Agent.ProfundidadSubtareas); err != nil {
		return err
	}
	if c.Agent.MaxTareas, err = leerEntero("STARLIGHT_AGENT_MAX_TAREAS", c.Agent.MaxTareas); err != nil {
		return err
	}
	c.Agent.WorkspaceDir = leerTexto("STARLIGHT_AGENT_WORKSPACE_DIR", c.Agent.WorkspaceDir)
	c.Agent.LogFile = leerTexto("STARLIGHT_AGENT_LOG_FILE", c.Agent.LogFile)
	c.Agent.LogNivel = leerTexto("STARLIGHT_AGENT_LOG_LEVEL", c.Agent.LogNivel)
	if c.Agent.LogConsola, err = leerBool("STARLIGHT_AGENT_LOG_CONSOLA", c.Agent.LogConsola); err != nil {
		return err
	}
	if c.Agent.LogMaxMB, err = leerEntero("STARLIGHT_AGENT_LOG_MAX_MB", c.Agent.LogMaxMB); err != nil {
		return err
	}
	if c.Agent.LogBackups, err = leerEntero("STARLIGHT_AGENT_LOG_BACKUPS", c.Agent.LogBackups); err != nil {
		return err
	}
	if c.Agent.ApagadoTimeout, err = leerDuracion("STARLIGHT_AGENT_APAGADO_TIMEOUT", c.Agent.ApagadoTimeout); err != nil {
		return err
	}

	return nil
}

func leerTexto(clave, actual string) string {
	if v, ok := os.LookupEnv(clave); ok {
		return strings.TrimSpace(v)
	}
	return actual
}

func leerDuracion(clave string, actual time.Duration) (time.Duration, error) {
	v, ok := os.LookupEnv(clave)
	if !ok || strings.TrimSpace(v) == "" {
		return actual, nil
	}
	d, err := aDuracion(strings.TrimSpace(v), clave)
	if err != nil {
		return actual, fmt.Errorf("%s: %v", clave, err)
	}
	return d, nil
}

func leerEntero(clave string, actual int) (int, error) {
	v, ok := os.LookupEnv(clave)
	if !ok || strings.TrimSpace(v) == "" {
		return actual, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return actual, fmt.Errorf("%s: %q no es un entero", clave, v)
	}
	return n, nil
}

func leerBool(clave string, actual bool) (bool, error) {
	v, ok := os.LookupEnv(clave)
	if !ok || strings.TrimSpace(v) == "" {
		return actual, nil
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "si", "sí", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return actual, fmt.Errorf("%s: %q no es un booleano (usa true/false)", clave, v)
	}
}
