package agent

import (
	"fmt"
	"os"
	"testing"

	"github.com/madkoding/starlight/internal/logx"
	"github.com/madkoding/starlight/internal/sandbox"
)

// TestMain permite que el binario de pruebas actúe como proceso hijo del
// sandbox cuando lo invocan con la marca correspondiente. Sin esto, el sandbox
// re-ejecutaría el binario de pruebas, que volvería a lanzar toda la suite.
func TestMain(m *testing.M) {
	if sandbox.EsEjecucionHijo(os.Args[1:]) {
		if err := sandbox.EjecutarComoHijo(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "agent[pruebas]: %v\n", err)
			os.Exit(126)
		}
		return
	}
	l, _ := logx.Nuevo(logx.Opciones{Nivel: logx.Error, Consola: false})
	logx.Instalar(l)
	os.Exit(m.Run())
}
