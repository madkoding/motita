package plantilla

import (
	"strings"
	"testing"
)

func TestRenderSustituyeVariables(t *testing.T) {
	salida, faltan := Render("Tarea: {{tarea}} en {{workspace}}", map[string]string{
		"tarea":     "arreglar el test",
		"workspace": "/tmp/trabajo",
	})
	if salida != "Tarea: arreglar el test en /tmp/trabajo" {
		t.Errorf("salida = %q", salida)
	}
	if len(faltan) != 0 {
		t.Errorf("no debería faltar nada: %v", faltan)
	}
}

func TestRenderConEspacios(t *testing.T) {
	salida, _ := Render("{{ tarea }} y {{tarea}}", map[string]string{"tarea": "x"})
	if salida != "x y x" {
		t.Errorf("salida = %q", salida)
	}
}

// TestRenderReportaFaltantes: una variable sin valor no se sustituye por vacío en
// silencio; se avisa, porque un prompt con huecos hace fallar al modelo de forma
// desconcertante.
func TestRenderReportaFaltantes(t *testing.T) {
	salida, faltan := Render("{{tarea}} -- {{desconocida}} -- {{otra}}", map[string]string{"tarea": "x"})
	if len(faltan) != 2 {
		t.Fatalf("faltantes = %v", faltan)
	}
	if faltan[0] != "desconocida" || faltan[1] != "otra" {
		t.Errorf("faltantes desordenadas: %v", faltan)
	}
	if !strings.Contains(salida, "{{desconocida}}") {
		t.Errorf("la variable desconocida debe quedarse visible: %q", salida)
	}
}

func TestVariablesDetectadas(t *testing.T) {
	vars := Variables("{{a}} {{b}} {{a}} y {{ c }}")
	esperado := []string{"a", "b", "c"}
	if strings.Join(vars, ",") != strings.Join(esperado, ",") {
		t.Errorf("variables = %v", vars)
	}
}

func TestHistorialVacio(t *testing.T) {
	h := Historial(nil)
	if !strings.Contains(h, "primer intento") {
		t.Errorf("historial = %q", h)
	}
}

func TestHistorialConIntentos(t *testing.T) {
	h := Historial([]string{"el test 1 falló", "el test 2 falló"})
	if !strings.Contains(h, "Intento fallido 1") || !strings.Contains(h, "Intento fallido 2") {
		t.Errorf("historial = %q", h)
	}
	if !strings.Contains(h, "el test 1 falló") {
		t.Errorf("falta el contenido del intento: %q", h)
	}
}
