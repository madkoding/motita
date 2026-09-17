package task

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
)

func init() {
	l, _ := logx.Nuevo(logx.Opciones{Nivel: logx.Error, Consola: false})
	logx.Instalar(l)
}

// --- archivo ----------------------------------------------------------------

func TestFuenteArchivo(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "tarea.md")
	if err := os.WriteFile(ruta, []byte("  arregla el bug del parser\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Nueva(config.TaskSource{Tipo: "file", Ruta: ruta}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	tarea, err := f.Siguiente(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tarea.Descripcion != "arregla el bug del parser" {
		t.Errorf("descripción = %q", tarea.Descripcion)
	}
	// La segunda llamada debe indicar el fin.
	if _, err := f.Siguiente(context.Background()); err != io.EOF {
		t.Errorf("se esperaba io.EOF, se obtuvo %v", err)
	}
}

func TestFuenteArchivoInexistente(t *testing.T) {
	f, _ := Nueva(config.TaskSource{Tipo: "file", Ruta: "/no/existe/tarea.md"}, logx.Global())
	if _, err := f.Siguiente(context.Background()); err == nil {
		t.Fatal("se esperaba un error de lectura")
	}
}

func TestFuenteArchivoVacio(t *testing.T) {
	ruta := filepath.Join(t.TempDir(), "vacio.md")
	os.WriteFile(ruta, []byte("   \n"), 0o644)
	f, _ := Nueva(config.TaskSource{Tipo: "file", Ruta: ruta}, logx.Global())
	if _, err := f.Siguiente(context.Background()); err != io.EOF {
		t.Errorf("un archivo vacío debe terminar la fuente, se obtuvo %v", err)
	}
}

// --- cola -------------------------------------------------------------------

// TestFuenteColaOrdenYConsumo: los archivos se toman en orden alfabético y se
// marcan como hechos, para no repetir trabajo tras un reinicio.
func TestFuenteColaOrdenYConsumo(t *testing.T) {
	dir := t.TempDir()
	for _, nombre := range []string{"003-c.txt", "001-a.txt", "002-b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, nombre), []byte("tarea "+nombre), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := Nueva(config.TaskSource{Tipo: "queue", Dir: dir, Intervalo: time.Millisecond}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}

	var recogidas []string
	for i := 0; i < 3; i++ {
		tarea, err := f.Siguiente(context.Background())
		if err != nil {
			t.Fatalf("error en la tarea %d: %v", i, err)
		}
		recogidas = append(recogidas, tarea.Descripcion)
	}
	esperado := []string{"tarea 001-a.txt", "tarea 002-b.txt", "tarea 003-c.txt"}
	if strings.Join(recogidas, "|") != strings.Join(esperado, "|") {
		t.Errorf("orden = %v", recogidas)
	}

	// Los originales deben haberse consumido.
	entradas, _ := os.ReadDir(dir)
	hechos := 0
	for _, e := range entradas {
		if strings.HasSuffix(e.Name(), ".hecho") {
			hechos++
		}
	}
	if hechos != 3 {
		t.Errorf("archivos marcados como hechos = %d, se esperaban 3", hechos)
	}
}

// TestFuenteColaSeCancela: una cola vacía no debe bloquear el apagado.
func TestFuenteColaSeCancela(t *testing.T) {
	f, _ := Nueva(config.TaskSource{Tipo: "queue", Dir: t.TempDir(), Intervalo: 10 * time.Second}, logx.Global())
	ctx, cancelar := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelar()

	inicio := time.Now()
	_, err := f.Siguiente(ctx)
	if err != io.EOF {
		t.Errorf("con el contexto cancelado debe devolver io.EOF, se obtuvo %v", err)
	}
	if transcurrido := time.Since(inicio); transcurrido > 3*time.Second {
		t.Errorf("la cancelación no se respetó: %s", transcurrido)
	}
}

func TestFuenteColaDirInexistente(t *testing.T) {
	f, _ := Nueva(config.TaskSource{Tipo: "queue", Dir: "/no/existe/cola", Intervalo: time.Millisecond}, logx.Global())
	if _, err := f.Siguiente(context.Background()); err == nil {
		t.Fatal("se esperaba un error por directorio inexistente")
	}
}

// --- api --------------------------------------------------------------------

func TestFuenteAPIString(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tarea":"revisa los logs de la aplicación"}`)
	}))
	defer srv.Close()

	f, err := Nueva(config.TaskSource{Tipo: "api", URL: srv.URL, Campo: "tarea"}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	tarea, err := f.Siguiente(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tarea.Descripcion != "revisa los logs de la aplicación" {
		t.Errorf("descripción = %q", tarea.Descripcion)
	}
}

func TestFuenteAPIObjeto(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"tarea":{"descripcion":"genera el informe","contexto":{"zona":"sur"}}}`)
	}))
	defer srv.Close()

	f, _ := Nueva(config.TaskSource{Tipo: "api", URL: srv.URL, Campo: "tarea"}, logx.Global())
	tarea, err := f.Siguiente(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tarea.Descripcion != "genera el informe" {
		t.Errorf("descripción = %q", tarea.Descripcion)
	}
	if tarea.Contexto["zona"] != "sur" {
		t.Errorf("contexto = %v", tarea.Contexto)
	}
}

func TestFuenteAPITextoPlano(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "tarea en texto plano")
	}))
	defer srv.Close()

	f, _ := Nueva(config.TaskSource{Tipo: "api", URL: srv.URL}, logx.Global())
	tarea, err := f.Siguiente(context.Background())
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if tarea.Descripcion != "tarea en texto plano" {
		t.Errorf("descripción = %q", tarea.Descripcion)
	}
}

// TestFuenteAPI204Termina: sin contenido, la fuente termina en lugar de esperar
// eternamente.
func TestFuenteAPI204Termina(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	f, _ := Nueva(config.TaskSource{Tipo: "api", URL: srv.URL}, logx.Global())
	if _, err := f.Siguiente(context.Background()); err != io.EOF {
		t.Errorf("204 debe terminar, se obtuvo %v", err)
	}
}

// TestFuenteAPIReintenta: un fallo temporal no debe abortar la ejecución.
func TestFuenteAPIReintenta(t *testing.T) {
	var llamadas int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&llamadas, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, "caído")
			return
		}
		fmt.Fprint(w, `{"tarea":"al segundo intento"}`)
	}))
	defer srv.Close()

	f, _ := Nueva(config.TaskSource{Tipo: "api", URL: srv.URL, Intervalo: time.Millisecond}, logx.Global())
	tarea, err := f.Siguiente(context.Background())
	if err != nil {
		t.Fatalf("debería recuperarse: %v", err)
	}
	if tarea.Descripcion != "al segundo intento" {
		t.Errorf("descripción = %q", tarea.Descripcion)
	}
}

func TestFuenteAPICampoAusente(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"otra_cosa":1}`)
	}))
	defer srv.Close()

	f, _ := Nueva(config.TaskSource{Tipo: "api", URL: srv.URL, Campo: "tarea", Intervalo: time.Millisecond}, logx.Global())
	ctx, cancelar := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelar()
	if _, err := f.Siguiente(ctx); err != io.EOF {
		t.Errorf("debe reintentar y terminar con el contexto cancelado, se obtuvo %v", err)
	}
}

func TestFuenteAPICabecerasYPost(t *testing.T) {
	var (
		metodo   string
		cabecera string
		cuerpo   map[string]any
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metodo = r.Method
		cabecera = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&cuerpo)
		fmt.Fprint(w, `{"tarea":"x"}`)
	}))
	defer srv.Close()

	f, _ := Nueva(config.TaskSource{
		Tipo: "api", URL: srv.URL, Metodo: "POST", Campo: "tarea",
		Cuerpo:  `{"cola":"general"}`,
		Headers: map[string]string{"Authorization": "Bearer token"},
	}, logx.Global())

	if _, err := f.Siguiente(context.Background()); err != nil {
		t.Fatal(err)
	}
	if metodo != "POST" {
		t.Errorf("método = %q", metodo)
	}
	if cabecera != "Bearer token" {
		t.Errorf("cabecera = %q", cabecera)
	}
	if cuerpo["cola"] != "general" {
		t.Errorf("cuerpo = %v", cuerpo)
	}
}

// --- constructores ----------------------------------------------------------

func TestNuevaTextoYArchivo(t *testing.T) {
	f, err := NuevaTexto("haz esto", "prueba")
	if err != nil {
		t.Fatal(err)
	}
	tarea, _ := f.Siguiente(context.Background())
	if tarea.Descripcion != "haz esto" || tarea.Origen != "prueba" {
		t.Errorf("tarea = %+v", tarea)
	}

	ruta := filepath.Join(t.TempDir(), "t.txt")
	os.WriteFile(ruta, []byte("desde archivo"), 0o644)
	fa, err := NuevaArchivo(ruta)
	if err != nil {
		t.Fatal(err)
	}
	tarea2, _ := fa.Siguiente(context.Background())
	if tarea2.Descripcion != "desde archivo" {
		t.Errorf("tarea = %+v", tarea2)
	}
}

func TestTipoDesconocido(t *testing.T) {
	if _, err := Nueva(config.TaskSource{Tipo: "telepatia"}, logx.Global()); err == nil {
		t.Fatal("se esperaba un error")
	}
}

func TestFuenteStdindeDefecto(t *testing.T) {
	f, err := Nueva(config.TaskSource{}, logx.Global())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(f.Descripcion(), "stdin") {
		t.Errorf("descripción = %q", f.Descripcion())
	}
	f.Cerrar()
}
