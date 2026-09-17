package anchor

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
)

func init() {
	// Silenciar el registro global durante las pruebas.
	l, _ := logx.Nuevo(logx.Opciones{Nivel: logx.Error, Consola: false})
	logx.Instalar(l)
}

// TestAnclaPASSConComandoReal comprueba que el ancla ejecuta de verdad.
func TestAnclaPASSConComandoReal(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Anchor{
		Tipo:        "command",
		Comando:     "sh",
		Argumentos:  []string{"-c", "echo todo-bien"},
		Timeout:     10 * time.Second,
		EsperarExit: 0,
	}

	res := Nuevo(cfg, dir, nil).Validar(context.Background())
	if !res.PASS {
		t.Fatalf("se esperaba PASS, motivo: %s", res.Motivo)
	}
	if len(res.Checks) != 1 || res.Checks[0].Salida != "todo-bien\n" {
		t.Errorf("check inesperado: %+v", res.Checks)
	}
}

// TestAnclaFAILPorExit verifica que un código distinto de cero es FAIL y que el
// motivo lo dice con claridad.
func TestAnclaFAILPorExit(t *testing.T) {
	cfg := config.Anchor{
		Tipo:        "command",
		Comando:     "sh",
		Argumentos:  []string{"-c", "echo fallo && exit 3"},
		Timeout:     10 * time.Second,
		EsperarExit: 0,
	}

	res := Nuevo(cfg, t.TempDir(), nil).Validar(context.Background())
	if res.PASS {
		t.Fatal("un exit 3 no puede dar PASS")
	}
	if res.Checks[0].Exit != 3 {
		t.Errorf("exit registrado = %d", res.Checks[0].Exit)
	}
	if res.Checks[0].Error == "" {
		t.Error("el registro del check debe explicar el fallo")
	}
}

// TestAnclaEsperarSalida comprueba la validación por expresión regular, que es
// lo que permite exigir un contrato de salida y no sólo un exit code.
func TestAnclaEsperarSalida(t *testing.T) {
	casos := []struct {
		nombre string
		salida string
		patrón string
		espera bool
	}{
		{"coincide", "INFORME_OK filas=120\n", `INFORME_OK`, true},
		{"no coincide", "todo mal\n", `INFORME_OK`, false},
		{"expresión con números", "filas=120\n", `filas=\d+`, true},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			cfg := config.Anchor{
				Tipo:          "command",
				Comando:       "sh",
				Argumentos:    []string{"-c", "printf " + "'" + caso.salida + "'"},
				Timeout:       10 * time.Second,
				EsperarExit:   0,
				EsperarSalida: caso.patrón,
			}
			res := Nuevo(cfg, t.TempDir(), nil).Validar(context.Background())
			if res.PASS != caso.espera {
				t.Errorf("PASS = %v, se esperaba %v (motivo: %s)", res.PASS, caso.espera, res.Motivo)
			}
		})
	}
}

// TestAnclaMultiplesChecksTodasDebenPasar: una sola comprobación fallida
// invalida el resultado completo.
func TestAnclaMultiplesChecksTodasDebenPasar(t *testing.T) {
	cfg := config.Anchor{
		Tipo:        "command",
		Comando:     "sh",
		Argumentos:  []string{"-c", "exit 0"},
		Timeout:     10 * time.Second,
		EsperarExit: 0,
		Checks: []config.Check{
			{Nombre: "dos", Comando: "sh", Argumentos: []string{"-c", "exit 0"}, Timeout: 5 * time.Second, EsperarExit: 0},
			{Nombre: "tres", Comando: "sh", Argumentos: []string{"-c", "exit 1"}, Timeout: 5 * time.Second, EsperarExit: 0},
		},
	}
	res := Nuevo(cfg, t.TempDir(), nil).Validar(context.Background())
	if res.PASS {
		t.Fatal("con un check fallido no puede haber PASS")
	}
	if len(res.Checks) != 3 {
		t.Errorf("se esperaban 3 checks, hubo %d", len(res.Checks))
	}
	if res.Checks[1].PASS != true || res.Checks[2].PASS != false {
		t.Errorf("estados inesperados: %+v", res.Checks)
	}
}

// TestAnclaTipoNoneNoDaPASSOptimista: sin configuración no se valida, así que no
// se puede declarar PASS.
func TestAnclaTipoNone(t *testing.T) {
	res := Nuevo(config.Anchor{Tipo: "none"}, t.TempDir(), nil).Validar(context.Background())
	if res.PASS {
		t.Fatal("anchor.tipo=none nunca debe dar PASS")
	}
	if res.Motivo == "" {
		t.Error("debe explicar que no se aplicó validación")
	}
}

// TestAnclaTimeout: un comando que no termina no puede colgar al agente.
func TestAnclaTimeout(t *testing.T) {
	cfg := config.Anchor{
		Tipo:        "command",
		Comando:     "sh",
		Argumentos:  []string{"-c", "sleep 30"},
		Timeout:     1 * time.Second,
		EsperarExit: 0,
	}
	inicio := time.Now()
	res := Nuevo(cfg, t.TempDir(), nil).Validar(context.Background())
	if res.PASS {
		t.Fatal("un timeout debe ser FAIL")
	}
	if transcurrido := time.Since(inicio); transcurrido > 5*time.Second {
		t.Errorf("el timeout no se aplicó a tiempo: %s", transcurrido)
	}
	if res.Checks[0].Error == "" {
		t.Error("debe registrar el error del timeout")
	}
}

// TestAnclaComandoInexistente: un comando que no existe es un FAIL explicable,
// no un pánico.
func TestAnclaComandoInexistente(t *testing.T) {
	cfg := config.Anchor{
		Tipo:        "command",
		Comando:     "/no/existe/este/comando",
		Timeout:     5 * time.Second,
		EsperarExit: 0,
	}
	res := Nuevo(cfg, t.TempDir(), nil).Validar(context.Background())
	if res.PASS {
		t.Fatal("un comando inexistente debe ser FAIL")
	}
	if res.Checks[0].Error == "" {
		t.Error("el error debe quedar registrado")
	}
}

// TestAnclaRegexInvalida: una expresión regular mal escrita se detecta y se
// reporta, en lugar de aceptar cualquier salida en silencio.
func TestAnclaRegexInvalida(t *testing.T) {
	cfg := config.Anchor{
		Tipo:          "command",
		Comando:       "true",
		Timeout:       5 * time.Second,
		EsperarSalida: "(sin cerrar",
	}
	res := Nuevo(cfg, t.TempDir(), nil).Validar(context.Background())
	if res.PASS {
		t.Fatal("una regex inválida debe impedir el PASS")
	}
	if res.Checks[0].Error == "" {
		t.Error("debe explicar que la regex no compila")
	}
}

// TestAnclaTrabajaEnElDirectorioIndicado: el ancla debe ver los archivos del
// intento, no los del directorio desde el que se lanzó el agente.
func TestAnclaTrabajaEnElDirectorioIndicado(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marca.txt"), []byte("aqui"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Anchor{
		Tipo:        "command",
		Comando:     "sh",
		Argumentos:  []string{"-c", "cat marca.txt"},
		Timeout:     10 * time.Second,
		EsperarExit: 0,
	}
	res := Nuevo(cfg, dir, nil).Validar(context.Background())
	if !res.PASS {
		t.Fatalf("no encontró el archivo del directorio de trabajo: %s", res.Motivo)
	}
}

// TestResultadoJSON: el registro estructurado debe ser JSON válido, porque es lo
// que se le pasa al LLM en el siguiente intento.
func TestResultadoJSON(t *testing.T) {
	cfg := config.Anchor{Tipo: "command", Comando: "true", Timeout: 5 * time.Second}
	res := Nuevo(cfg, t.TempDir(), nil).Validar(context.Background())
	texto := res.JSON()
	if len(texto) == 0 || texto[0] != '{' {
		t.Fatalf("JSON inválido: %s", texto)
	}
	if !contains(texto, `"pass"`) {
		t.Errorf("el JSON debe incluir el veredicto: %s", texto)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
