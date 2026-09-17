package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCargarSinClaveNoSustituyeElArchivo: el fallo que esto previene, encontrado
// al probar el binario de verdad, era que `-validar-config` con un YAML inválido
// informaba "configuración válida" y salía con 0, porque la carga fallida se
// sustituía en silencio por los valores por defecto. Ocultaba exactamente el
// error que el modo de validación existe para detectar.
func TestCargarSinClaveNoSustituyeElArchivo(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "mala.yaml")
	if err := os.WriteFile(ruta, []byte("task_source:\n  tipo: telepatia\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := CargarSinClave(ruta); err == nil {
		t.Fatal("un tipo de fuente desconocido debe fallar aunque no se exija la clave")
	} else if !strings.Contains(err.Error(), "telepatia") {
		t.Errorf("el error debe decir qué valor es inválido: %v", err)
	}

	if _, err := Cargar(ruta); err == nil {
		t.Fatal("Cargar también debe fallar")
	}
}

func TestCargarSinClaveToleraLaClaveAusente(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "sin-clave.yaml")
	contenido := "anchor:\n  tipo: command\n  comando: make\n"
	if err := os.WriteFile(ruta, []byte(contenido), 0o644); err != nil {
		t.Fatal(err)
	}

	// Sin clave: Cargar falla, CargarSinClave no.
	if _, err := Cargar(ruta); err == nil {
		t.Fatal("Cargar debe exigir la clave")
	}
	cfg, err := CargarSinClave(ruta)
	if err != nil {
		t.Fatalf("CargarSinClave no debe exigir la clave: %v", err)
	}
	if cfg.Anchor.Comando != "make" {
		t.Errorf("el archivo no se leyó: %+v", cfg.Anchor)
	}
}

func TestCargarSinClaveSigueValidandoTodoLoDemas(t *testing.T) {
	casos := []struct {
		nombre    string
		contenido string
		contiene  string
	}{
		{"proveedor desconocido", "llm:\n  proveedor: mago\n", "llm.proveedor desconocido"},
		{"sandbox desconocido", "sandbox:\n  tipo: magia\n", "sandbox.tipo desconocido"},
		{"chroot sin raiz", "sandbox:\n  tipo: chroot\n", "requiere 'raiz'"},
		{"clave desconocida", "llm:\n  color: azul\n", "color"},
		{"nivel invalido", "agent:\n  log_level: verboso\n", "agent.log_level desconocido"},
	}
	for _, caso := range casos {
		t.Run(caso.nombre, func(t *testing.T) {
			ruta := filepath.Join(t.TempDir(), "c.yaml")
			os.WriteFile(ruta, []byte(caso.contenido), 0o644)
			_, err := CargarSinClave(ruta)
			if err == nil {
				t.Fatalf("se esperaba un error para %q", caso.contenido)
			}
			if !strings.Contains(err.Error(), caso.contiene) {
				t.Errorf("error = %q, se esperaba que contuviera %q", err, caso.contiene)
			}
		})
	}
}

func TestArchivoInexistenteFalla(t *testing.T) {
	if _, err := CargarSinClave("/no/existe/config.yaml"); err == nil {
		t.Fatal("un archivo inexistente debe fallar, no caer a los valores por defecto")
	}
}
