//go:build !linux

package sandbox

// Fuera de Linux no hay ulimit en el mismo sentido: el shell del sistema decide.
// Se devuelve el comando sin envolver y el padre registra que no se aplican
// límites de proceso (sólo se aísla el directorio temporal).
func envolverConUlimit(l Limites, comando string, args []string) (string, []string) {
	return comando, append([]string{comando}, args...)
}

func minimaMemoriaNecesaria() int { return 0 }

func picoMemoriaVirtual() uint64 { return 0 }
