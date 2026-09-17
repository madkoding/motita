// Package execx contiene las primitivas de ejecución de procesos compartidas
// por el ancla (Capa A) y el sandbox (Capa C).
//
// Aquí vive lo que costó sangre: matar de verdad a un comando que expira.
// `exec.CommandContext` sólo mata al proceso directo; sus hijos (`sleep`, un
// script con pipes) siguen vivos con el tubo de salida abierto y `Wait` se
// queda esperando mucho más allá del plazo. Verificado: un `sleep 30` con
// timeout de 1 s tardaba 5 s. La solución es grupo de procesos propio,
// SIGKILL al grupo y un WaitDelay acotado.
package execx

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Peticion describe un comando a ejecutar.
type Peticion struct {
	Comando         string
	Args            []string
	Dir             string
	Entorno         []string // nil = heredar el del proceso
	Timeout         time.Duration
	MaxSalida       int64 // bytes de salida combinada (0 = 256 KiB)
	EntradaEstandar []byte
}

// Resultado de una ejecución.
type Resultado struct {
	Salida   string
	Exit     int
	Truncado bool
	Duracion time.Duration
	Expirado bool
}

// bufferLimitado acumula hasta max bytes y marca si hubo recorte, sin hacer
// fallar al proceso que escribe.
type bufferLimitado struct {
	buf      bytes.Buffer
	max      int64
	truncado bool
}

func (b *bufferLimitado) Write(p []byte) (int, error) {
	espacio := b.max - int64(b.buf.Len())
	if espacio <= 0 {
		b.truncado = true
		return len(p), nil
	}
	if espacio < int64(len(p)) {
		b.buf.Write(p[:espacio])
		b.truncado = true
		return len(p), nil
	}
	b.buf.Write(p)
	return len(p), nil
}

// Ejecutar corre el comando con grupo de procesos y timeout real. Devuelve la
// salida combinada (stdout+stderr), el código de salida y un error explícito si
// el comando no pudo lanzarse, expiró o terminó con código distinto de cero.
func Ejecutar(ctx context.Context, p Peticion) (string, bool, int, error) {
	if strings.TrimSpace(p.Comando) == "" {
		return "", false, -1, fmt.Errorf("comando vacío")
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	max := p.MaxSalida
	if max <= 0 {
		max = 256 << 10
	}

	ctxHijo, cancelar := context.WithTimeout(ctx, timeout)
	defer cancelar()

	cmd := exec.CommandContext(ctxHijo, p.Comando, p.Args...)
	cmd.Dir = p.Dir
	if p.Entorno != nil {
		cmd.Env = p.Entorno
	}
	if p.EntradaEstandar != nil {
		cmd.Stdin = bytes.NewReader(p.EntradaEstandar)
	}

	configurarGrupo(cmd)
	cmd.Cancel = func() error { return matarGrupo(cmd) }
	cmd.WaitDelay = 2 * time.Second

	salida := &bufferLimitado{max: max}
	cmd.Stdout = salida
	cmd.Stderr = salida

	inicio := time.Now()
	err := cmd.Run()
	duracion := time.Since(inicio)
	texto := salida.buf.String()

	if ctxHijo.Err() == context.DeadlineExceeded {
		return texto, salida.truncado, -1, fmt.Errorf("el comando excedió el límite de %s y fue terminado (grupo de procesos enviado a SIGKILL)", timeout)
	}

	exit := 0
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			exit = ee.ExitCode()
			if exit < 0 {
				// Terminado por señal (p. ej. SIGKILL por parte del sandbox).
				return texto, salida.truncado, exit, fmt.Errorf("el comando terminó por una señal (exit=%d) tras %s", exit, duracion.Round(time.Millisecond))
			}
			return texto, salida.truncado, exit, nil
		}
		return texto, salida.truncado, -1, fmt.Errorf("no se pudo ejecutar %q: %w", p.Comando, err)
	}
	_ = duracion
	return texto, salida.truncado, exit, nil
}

// EjecutarShell corre el comando a través de `sh -c`, para admitir pipes,
// redirecciones y comillas como lo haría una persona en la terminal.
func EjecutarShell(ctx context.Context, comando, dir string, timeout time.Duration, max int64) (Resultado, error) {
	ctxHijo, cancelar := context.WithTimeout(ctx, timeout)
	defer cancelar()

	cmd := exec.CommandContext(ctxHijo, "sh", "-c", comando)
	cmd.Dir = dir
	configurarGrupo(cmd)
	cmd.Cancel = func() error { return matarGrupo(cmd) }
	cmd.WaitDelay = 2 * time.Second

	if max <= 0 {
		max = 256 << 10
	}
	salida := &bufferLimitado{max: max}
	cmd.Stdout = salida
	cmd.Stderr = salida

	inicio := time.Now()
	err := cmd.Run()
	res := Resultado{
		Salida:   salida.buf.String(),
		Truncado: salida.truncado,
		Duracion: time.Since(inicio),
	}

	if ctxHijo.Err() == context.DeadlineExceeded {
		res.Expirado = true
		res.Exit = -1
		return res, fmt.Errorf("el comando excedió el límite de %s", timeout)
	}

	if err != nil {
		var ee *exec.ExitError
		if asExitError(err, &ee) {
			res.Exit = ee.ExitCode()
			if res.Exit < 0 {
				return res, fmt.Errorf("el comando terminó por una señal (exit=%d)", res.Exit)
			}
			return res, nil
		}
		res.Exit = -1
		return res, fmt.Errorf("no se pudo ejecutar el comando: %w", err)
	}
	return res, nil
}

// LimpiarSalida normaliza la salida para registros y prompts.
func LimpiarSalida(s string) string {
	return strings.TrimRight(s, "\n")
}
