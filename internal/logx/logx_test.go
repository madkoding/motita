package logx

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRegistroEsJSONLineas: cada línea debe ser un objeto JSON parseable, que es
// lo que permite consumirlo con jq o un colector.
func TestRegistroEsJSONLineas(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "agente.log")
	l, err := Nuevo(Opciones{Ruta: ruta, Nivel: Debug, Consola: false, MaxMB: 10, Backups: 2})
	if err != nil {
		t.Fatal(err)
	}

	l.Debug("arrancando", "modelo", "gpt")
	l.Info("tarea recibida", "descripcion", "algo", "intento", 1)
	l.Warn("cuidado", "detalle", "x")
	l.Error("falló", "error", os.ErrNotExist)
	if err := l.Cerrar(); err != nil {
		t.Fatal(err)
	}

	lineas := leerLineas(t, ruta)
	if len(lineas) != 4 {
		t.Fatalf("se esperaban 4 líneas, hay %d", len(lineas))
	}
	for i, linea := range lineas {
		var evento map[string]any
		if err := json.Unmarshal([]byte(linea), &evento); err != nil {
			t.Fatalf("línea %d no es JSON: %v (%q)", i, err, linea)
		}
		for _, campo := range []string{"ts", "nivel", "msg"} {
			if _, ok := evento[campo]; !ok {
				t.Errorf("línea %d sin el campo %q: %s", i, campo, linea)
			}
		}
	}

	var primero map[string]any
	json.Unmarshal([]byte(lineas[0]), &primero)
	if primero["nivel"] != "debug" {
		t.Errorf("nivel = %v", primero["nivel"])
	}

	// Un error debe serializarse como texto, no romper el JSON.
	var cuarto map[string]any
	json.Unmarshal([]byte(lineas[3]), &cuarto)
	if _, ok := cuarto["error"].(string); !ok {
		t.Errorf("el error debe guardarse como texto: %#v", cuarto["error"])
	}
}

// TestNivelFiltra: registrar en debug no debe aparecer cuando el nivel es info.
func TestNivelFiltra(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "filtrado.log")
	l, _ := Nuevo(Opciones{Ruta: ruta, Nivel: Info, Consola: false})
	l.Debug("no debe aparecer")
	l.Info("sí debe aparecer")
	l.Cerrar()

	lineas := leerLineas(t, ruta)
	if len(lineas) != 1 {
		t.Fatalf("líneas = %d", len(lineas))
	}
	if !strings.Contains(lineas[0], "sí debe aparecer") {
		t.Errorf("línea = %s", lineas[0])
	}
}

// TestRotacionPorTamano: al superar el límite, el archivo se rota y se conservan
// los backups indicados, sin perder registros por el camino.
func TestRotacionPorTamano(t *testing.T) {
	dir := t.TempDir()
	ruta := filepath.Join(dir, "rotativo.log")

	// MaxMB en 0 desactivaría la rotación; se usa un tamaño diminuto simulando
	// el límite directamente.
	l, err := Nuevo(Opciones{Ruta: ruta, Nivel: Info, Consola: false, MaxMB: 1, Backups: 2})
	if err != nil {
		t.Fatal(err)
	}
	l.maxByte = 2048 // límite pequeño para la prueba

	for i := 0; i < 200; i++ {
		l.Info("mensaje de relleno para forzar la rotación", "i", i, "relleno", strings.Repeat("x", 100))
	}
	l.Cerrar()

	rotados := l.ArchivosRotados()
	if len(rotados) == 0 {
		t.Fatal("no se rotó el archivo")
	}
	if len(rotados) > 2 {
		t.Errorf("se conservan más backups de los configurados: %v", rotados)
	}
	// El archivo actual y los backups deben ser JSON válido.
	for _, r := range append([]string{ruta}, rotados...) {
		for i, linea := range leerLineas(t, r) {
			var evento map[string]any
			if err := json.Unmarshal([]byte(linea), &evento); err != nil {
				t.Errorf("%s línea %d ilegible: %v", r, i, err)
			}
		}
	}
}

// TestSinRotacionCuandoNoHaceFalta: no debe crear backups si no se llena.
func TestSinRotacionCuandoNoHaceFalta(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "pequeno.log")
	l, _ := Nuevo(Opciones{Ruta: ruta, Nivel: Info, Consola: false, MaxMB: 10, Backups: 3})
	l.Info("una sola línea")
	l.Cerrar()

	if rotados := l.ArchivosRotados(); len(rotados) != 0 {
		t.Errorf("no debería haber rotado: %v", rotados)
	}
}

// TestNivelInvalido: un nivel mal escrito se detecta.
func TestNivelInvalido(t *testing.T) {
	if _, err := ParsearNivel("verboso"); err == nil {
		t.Fatal("se esperaba un error")
	}
	if n, err := ParsearNivel("WARN"); err != nil || n != Warn {
		t.Errorf("WARN -> %v %v", n, err)
	}
	if n, err := ParsearNivel(""); err != nil || n != Info {
		t.Errorf("vacío debe ser info: %v %v", n, err)
	}
}

// TestSoloConsola: sin archivo, el registro no falla y escribe en la salida.
func TestSoloConsola(t *testing.T) {
	var sb strings.Builder
	l, err := Nuevo(Opciones{Nivel: Info, Consola: true})
	if err != nil {
		t.Fatal(err)
	}
	l.salida = &sb
	l.Info("sólo consola")
	if !strings.Contains(sb.String(), "sólo consola") {
		t.Errorf("salida = %q", sb.String())
	}
	if err := l.Cerrar(); err != nil {
		t.Errorf("cerrar sin archivo no debe fallar: %v", err)
	}
}

// TestConcurrencia: el registro se usa desde varias goroutines (el bucle del
// agente y el apagado), así que debe ser seguro.
func TestConcurrencia(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "concurrente.log")
	l, _ := Nuevo(Opciones{Ruta: ruta, Nivel: Info, Consola: false})

	hecho := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(n int) {
			for j := 0; j < 50; j++ {
				l.Info("desde goroutine", "g", n, "j", j)
			}
			hecho <- struct{}{}
		}(i)
	}
	for i := 0; i < 8; i++ {
		<-hecho
	}
	l.Cerrar()

	lineas := leerLineas(t, ruta)
	if len(lineas) != 400 {
		t.Errorf("líneas = %d, se esperaban 400 (¿se perdieron registros?)", len(lineas))
	}
	for i, linea := range lineas {
		var evento map[string]any
		if err := json.Unmarshal([]byte(linea), &evento); err != nil {
			t.Fatalf("línea %d corrupta (escritura concurrente): %v", i, err)
		}
	}
}

// TestRutaNoEscritible: un directorio imposible debe dar un error claro al
// construir el logger, no un registro silencioso.
func TestRutaNoEscritible(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("como root casi todo es escribible")
	}
	if _, err := Nuevo(Opciones{Ruta: "/proc/1/no-se-puede/registro.log", Nivel: Info}); err == nil {
		t.Fatal("se esperaba un error al abrir una ruta imposible")
	}
}

func leerLineas(t *testing.T, ruta string) []string {
	t.Helper()
	f, err := os.Open(ruta)
	if err != nil {
		t.Fatalf("no se pudo abrir %s: %v", ruta, err)
	}
	defer f.Close()

	var lineas []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) != "" {
			lineas = append(lineas, sc.Text())
		}
	}
	return lineas
}
