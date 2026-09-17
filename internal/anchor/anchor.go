// Package anchor implementa la Capa A: el validador determinista.
//
// No razona y no llama a ningún LLM: ejecuta comprobaciones reales (comandos,
// invariantes, esperas de salida) sobre el resultado del agente y devuelve
// PASS/FAIL con registros estructurados. Si esta capa no puede ejecutarse, el
// resultado es FAIL, nunca un PASS optimista.
package anchor

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/execx"
	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
)

// Resultado es el veredicto del ancla.
type Resultado struct {
	PASS       bool       `json:"pass"`
	Checks     []CheckLog `json:"checks"`
	DuracionMS int64      `json:"duracion_ms"`
	Motivo     string     `json:"motivo"`
}

// CheckLog es el registro estructurado de una comprobación.
type CheckLog struct {
	Nombre     string `json:"nombre"`
	Comando    string `json:"comando"`
	Exit       int    `json:"exit"`
	PASS       bool   `json:"pass"`
	DuracionMS int64  `json:"duracion_ms"`
	Salida     string `json:"salida,omitempty"`
	Error      string `json:"error,omitempty"`
	Truncado   bool   `json:"salida_truncada,omitempty"`
}

// Ancla valida resultados según la configuración.
type Ancla struct {
	cfg     config.Anchor
	dir     string // directorio de trabajo donde corren las comprobaciones
	sandbox *sandbox.Sandbox
	log     *logx.Logger
}

// Nuevo construye un ancla. El sandbox es opcional: si es nil, las
// comprobaciones corren directamente en el sistema.
//
// Nota de diseño: el ancla es la autoridad, así que por defecto corre las
// comprobaciones fuera del sandbox del agente. Si el sandbox tuviera un fallo,
// también lo tendría el validador, y un validador que falla en silencio deja
// pasar resultados malos. Cuando se le pasa un sandbox, es porque quien lo usa
// decidió aislar también la validación (por ejemplo, con el sandbox en chroot).
func Nuevo(cfg config.Anchor, dir string, caja *sandbox.Sandbox) *Ancla {
	return &Ancla{cfg: cfg, dir: dir, sandbox: caja, log: logx.Global()}
}

// Validador abstracto, para poder sustituirlo en las pruebas.
type Validador interface {
	Validar(ctx context.Context) Resultado
}

// Validar ejecuta todas las comprobaciones. Devuelve PASS sólo si todas pasan.
func (a *Ancla) Validar(ctx context.Context) Resultado {
	inicio := time.Now()
	res := Resultado{PASS: true, Checks: []CheckLog{}}

	if strings.EqualFold(a.cfg.Tipo, "none") || a.cfg.Tipo == "" {
		// Sin validación no hay verificación posible, así que NO se puede
		// declarar PASS. Un PASS sin comprobación real sería exactamente el
		// fallo que esta arquitectura existe para evitar.
		res.PASS = false
		res.Motivo = "anchor.tipo=none: no hay validación determinista, el resultado NO puede darse por verificado"
		res.DuracionMS = time.Since(inicio).Milliseconds()
		a.log.Error("ancla desactivada: no se puede verificar el resultado", "motivo", res.Motivo)
		return res
	}

	checks := a.comprobaciones()
	for _, c := range checks {
		registro := a.ejecutarCheck(ctx, c)
		res.Checks = append(res.Checks, registro)
		if !registro.PASS {
			res.PASS = false
		}
	}

	if res.PASS {
		res.Motivo = fmt.Sprintf("%d comprobación(es) superadas", len(res.Checks))
	} else {
		var fallidas []string
		for _, c := range res.Checks {
			if !c.PASS {
				fallidas = append(fallidas, c.Nombre)
			}
		}
		res.Motivo = "comprobaciones fallidas: " + strings.Join(fallidas, ", ")
	}
	res.DuracionMS = time.Since(inicio).Milliseconds()

	campos := []any{"pass", res.PASS, "motivo", res.Motivo, "duracion_ms", res.DuracionMS}
	if res.PASS {
		a.log.Info("validación del ancla", campos...)
	} else {
		a.log.Warn("validación del ancla", campos...)
	}
	return res
}

// comprobaciones normaliza la configuración en una lista homogénea.
func (a *Ancla) comprobaciones() []config.Check {
	lista := make([]config.Check, 0, 1+len(a.cfg.Checks))
	if a.cfg.Comando != "" {
		lista = append(lista, config.Check{
			Nombre:        "principal",
			Comando:       a.cfg.Comando,
			Argumentos:    a.cfg.Argumentos,
			Timeout:       a.cfg.Timeout,
			EsperarExit:   a.cfg.EsperarExit,
			EsperarSalida: a.cfg.EsperarSalida,
		})
	}
	lista = append(lista, a.cfg.Checks...)
	return lista
}

// ejecutarCheck corre una comprobación y evalúa exit code y salida esperada.
func (a *Ancla) ejecutarCheck(ctx context.Context, c config.Check) CheckLog {
	nombre := c.Nombre
	if nombre == "" {
		nombre = "check"
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}

	linea := strings.TrimSpace(c.Comando + " " + strings.Join(c.Argumentos, " "))
	registro := CheckLog{Nombre: nombre, Comando: linea}

	inicio := time.Now()
	var (
		salida    string
		exit      int
		errSalida error
		truncado  bool
	)

	peticion := execx.Peticion{
		Comando: c.Comando,
		Args:    c.Argumentos,
		Dir:     a.dir,
		Timeout: timeout,
	}
	if a.sandbox != nil {
		salida, truncado, exit, errSalida = a.sandbox.Ejecutar(ctx, peticion)
	} else {
		salida, truncado, exit, errSalida = ejecutarDirecto(ctx, peticion)
	}

	registro.DuracionMS = time.Since(inicio).Milliseconds()
	registro.Exit = exit
	registro.Salida = recortar(salida, 4000)
	registro.Truncado = truncado

	if errSalida != nil {
		registro.PASS = false
		registro.Error = errSalida.Error()
		return registro
	}

	esperado := c.EsperarExit
	if exit != esperado {
		registro.PASS = false
		if registro.Error == "" {
			registro.Error = fmt.Sprintf("se esperaba un código de salida %d y se obtuvo %d", esperado, exit)
		}
		return registro
	}

	if c.EsperarSalida != "" {
		re, err := regexp.Compile(c.EsperarSalida)
		if err != nil {
			registro.PASS = false
			registro.Error = "esperar_salida no es una expresión regular válida: " + err.Error()
			return registro
		}
		if !re.MatchString(salida) {
			registro.PASS = false
			registro.Error = fmt.Sprintf("la salida no coincide con %q", c.EsperarSalida)
			return registro
		}
	}

	registro.PASS = true
	return registro
}

// ejecutarDirecto es el camino sin sandbox (comprobaciones del ancla cuando el
// sandbox está desactivado o no aplica).
func ejecutarDirecto(ctx context.Context, p execx.Peticion) (string, bool, int, error) {
	return execx.Ejecutar(ctx, p)
}

// recortar limita el tamaño de la salida guardada en el registro.
func recortar(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// JSON serializa el resultado para el registro estructurado.
func (r Resultado) JSON() string {
	datos, err := json.Marshal(r)
	if err != nil {
		return `{"pass":false,"motivo":"no se pudo serializar el resultado"}`
	}
	return string(datos)
}

// Asegura que exec.Command existe para los sistemas que no soportan sh -c.
var _ = exec.Command
