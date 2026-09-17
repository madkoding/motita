package sandbox

import (
	"fmt"
	"os"
	"testing"

	"github.com/madkoding/starlight/internal/logx"
)

// TestMain hace que el binario de pruebas se comporte como el binario real: si
// lo invocan en modo hijo del sandbox, aplica los límites y hace exec del
// comando, en lugar de lanzar la suite.
//
// Sin esto, el sandbox re-ejecutaría "el propio ejecutable" (que en las pruebas
// es el binario de pruebas), éste ignoraría la marca y volvería a correr todas
// las pruebas, que a su vez lanzarían más hijos: recursión infinita. Es
// exactamente el tipo de fallo que sólo aparece al ejecutar de verdad.
func TestMain(m *testing.M) {
	if EsEjecucionHijo(os.Args[1:]) {
		if err := EjecutarComoHijo(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox[pruebas]: %v\n", err)
			os.Exit(126)
		}
		return
	}

	l, _ := logx.Nuevo(logx.Opciones{Nivel: logx.Error, Consola: false})
	logx.Instalar(l)
	os.Exit(m.Run())
}
