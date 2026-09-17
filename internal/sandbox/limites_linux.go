//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Aplicación de límites mediante el shell.
//
// Los límites NO los aplica el proceso de aislamiento (que es este mismo binario,
// escrito en Go): lo hace el shell, que es un binario de C pequeño. Es una
// decisión deliberada, tomada a partir de mediciones:
//
//   - RLIMIT_AS cuenta también la memoria virtual que el runtime de Go mapea. Si
//     el propio proceso aplica el límite, cualquier reserva posterior (sysmon,
//     GC, resolver el PATH) lo mata con "fatal error: runtime: cannot allocate
//     memory". Medido: fallaba sólo en un runner con más núcleos y con la
//     instrumentación de carreras.
//   - RLIMIT_CPU no corta en el instante exacto de agotarse, sino en la
//     siguiente planificación del proceso; el runtime de Go puede reservar
//     memoria antes de que eso ocurra. Medido en una máquina de un núcleo: sólo
//     con RLIMIT_CPU (sin límite de memoria) un bucle infinito seguía vivo a los
//     12 s con `cpu_segundos: 2`.
//
// Con `sh -c 'ulimit ...; exec "$@"'` el proceso limitado es exactamente el del
// usuario, y el shell desaparece con el exec (no queda ningún proceso intermedio
// consumiendo el presupuesto).
// ---------------------------------------------------------------------------

// envolverConUlimit devuelve el comando y los argumentos que aplican los límites
// y ejecutan el comando real.
//
// Si no hay límites configurados, devuelve el comando sin envolver: no se paga
// el coste de un shell intermedio innecesario.
func envolverConUlimit(l Limites, comando string, args []string) (string, []string) {
	ordenes := ordenesUlimit(l)
	if len(ordenes) == 0 {
		return comando, append([]string{comando}, args...)
	}

	// `exec "$@"` ejecuta el comando con sus argumentos sin volver a interpretar
	// nada: el comando y sus argumentos se pasan posicionalmente después del
	// nombre del shell (argv[0] del shell es "sh", argv[1] pasa a $0, y el
	// comando y sus argumentos empiezan en $1).
	script := strings.Join(ordenes, "; ") + `; exec "$@"`

	argsFinales := []string{"sh", "-c", script, "starlight-sandbox", comando}
	argsFinales = append(argsFinales, args...)
	return "/bin/sh", argsFinales
}

// ordenesUlimit construye las órdenes de `ulimit`, ordenadas para que la salida
// sea determinista (y las pruebas puedan compararla).
//
// Los errores de `ulimit` no abortan el script a propósito: en algunos sistemas
// (contenedores, LSM) un recurso puede no ser modificable, y en ese caso es mejor
// ejecutar el comando sin ese límite —queda registrado por el padre— que impedir
// el trabajo. El `2>/dev/null` evita además que un mensaje del shell contamine la
// salida que lee el modelo.
func ordenesUlimit(l Limites) []string {
	var ordenes []string

	if l.CPUSegundos > 0 {
		ordenes = append(ordenes, fmt.Sprintf("ulimit -t %d 2>/dev/null", l.CPUSegundos))
	}
	if l.MemoriaMB > 0 {
		// -v (RLIMIT_AS) está en dash y en bash; es el que más importa aquí.
		ordenes = append(ordenes, fmt.Sprintf("ulimit -v %d 2>/dev/null", l.MemoriaMB<<10))
	}
	if l.Procesos > 0 {
		// -u (RLIMIT_NPROC) no existe en dash; se intenta sin abortar.
		ordenes = append(ordenes, fmt.Sprintf("ulimit -u %d 2>/dev/null", l.Procesos))
	}
	if l.ArchivosAbiertos > 0 {
		ordenes = append(ordenes, fmt.Sprintf("ulimit -n %d 2>/dev/null", l.ArchivosAbiertos))
	}
	if l.TamanoArchivoMB > 0 {
		// -f está en bloques de 512 bytes.
		bloques := l.TamanoArchivoMB << 11
		ordenes = append(ordenes, fmt.Sprintf("ulimit -f %d 2>/dev/null", bloques))
	}

	sort.Strings(ordenes)
	return ordenes
}

// minimaMemoriaNecesaria devuelve el espacio de direcciones que el comando debería
// poder usar como mínimo, en MB: un múltiplo del pico del proceso que lanza el
// sandbox.
//
// Existe porque RLIMIT_AS por debajo de lo que ya está mapeado hace que el comando
// no pueda ni arrancar (y, si lo aplicara un binario de Go, mataría al propio
// runtime). El padre lo usa para ajustar y avisar en lugar de dejar el comando
// inservible en silencio.
//
// En las máquinas objetivo (i386 con poca RAM) el valor es pequeño; en un runner
// de CI con muchos núcleos es mayor, y de ahí que el ajuste tenga que ser
// dinámico y no una constante.
func minimaMemoriaNecesaria() int {
	// Un solo hilo y sin GC: se reduce lo que el runtime reserva en el periodo
	// que va desde aquí hasta el exec.
	runtime.GOMAXPROCS(1)
	debug.SetGCPercent(-1)

	pico := picoMemoriaVirtual()
	if pico == 0 {
		return 0
	}
	// Margen x2 sobre el pico observado.
	return int(pico>>20) * 2
}

// picoMemoriaVirtual lee el pico de memoria virtual del proceso actual, en bytes.
// Devuelve 0 si no se puede determinar (fuera de Linux no se usa).
func picoMemoriaVirtual() uint64 {
	datos, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, linea := range strings.Split(string(datos), "\n") {
		if !strings.HasPrefix(linea, "VmPeak:") {
			continue
		}
		campos := strings.Fields(linea)
		if len(campos) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(campos[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb << 10
	}
	return 0
}
