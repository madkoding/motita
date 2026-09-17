// Package task obtiene las tareas de la fuente configurada: stdin, archivo,
// cola de directorio o API HTTP.
//
// La fuente es una abstracción pequeña para que el resto del agente no sepa de
// dónde viene el trabajo, y para poder sustituirla en las pruebas.
package task

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/madkoding/starlight/internal/config"
	"github.com/madkoding/starlight/internal/logx"
)

// Tarea es una unidad de trabajo.
type Tarea struct {
	Descripcion string            `json:"descripcion"`
	Contexto    map[string]string `json:"contexto,omitempty"`
	Origen      string            `json:"origen,omitempty"`
}

// Fuente entrega tareas hasta que devuelve io.EOF.
type Fuente interface {
	// Siguiente devuelve la próxima tarea. Devuelve io.EOF cuando ya no hay más.
	Siguiente(ctx context.Context) (Tarea, error)
	// Cerrar libera recursos (archivos, conexiones).
	Cerrar() error
	// Descripcion describe la fuente para el registro.
	Descripcion() string
}

// Nueva construye la fuente según la configuración.
func Nueva(cfg config.TaskSource, log *logx.Logger) (Fuente, error) {
	if log == nil {
		log = logx.Global()
	}
	switch strings.ToLower(cfg.Tipo) {
	case "", "stdin":
		return &fuenteStdin{lector: bufio.NewReader(os.Stdin)}, nil
	case "file":
		return &fuenteArchivo{ruta: cfg.Ruta}, nil
	case "queue":
		return &fuenteCola{dir: cfg.Dir, intervalo: cfg.Intervalo, log: log}, nil
	case "api":
		return newFuenteAPI(cfg, log)
	default:
		return nil, fmt.Errorf("fuente de tareas desconocida: %q", cfg.Tipo)
	}
}

// --- stdin ------------------------------------------------------------------

type fuenteStdin struct {
	lector *bufio.Reader
}

func (f *fuenteStdin) Descripcion() string { return "stdin" }
func (f *fuenteStdin) Cerrar() error       { return nil }

func (f *fuenteStdin) Siguiente(ctx context.Context) (Tarea, error) {
	// Se leen líneas hasta tener una tarea completa (las vacías se saltan).
	for {
		linea, err := f.lector.ReadString('\n')
		texto := strings.TrimSpace(linea)
		if texto != "" {
			return Tarea{Descripcion: texto, Origen: "stdin"}, nil
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return Tarea{}, io.EOF
			}
			return Tarea{}, fmt.Errorf("error leyendo de stdin: %w", err)
		}
		select {
		case <-ctx.Done():
			return Tarea{}, ctx.Err()
		default:
		}
	}
}

// --- archivo ----------------------------------------------------------------

type fuenteArchivo struct {
	ruta  string
	hecho bool
}

func (f *fuenteArchivo) Descripcion() string { return "file:" + f.ruta }
func (f *fuenteArchivo) Cerrar() error       { return nil }

// Siguiente lee el archivo una sola vez y devuelve una tarea con su contenido.
func (f *fuenteArchivo) Siguiente(ctx context.Context) (Tarea, error) {
	if f.hecho {
		return Tarea{}, io.EOF
	}
	f.hecho = true

	datos, err := os.ReadFile(f.ruta)
	if err != nil {
		return Tarea{}, fmt.Errorf("no se pudo leer la tarea en %q: %w", f.ruta, err)
	}
	texto := strings.TrimSpace(string(datos))
	if texto == "" {
		return Tarea{}, io.EOF
	}

	// Si el archivo es JSON con una lista de tareas, cada elemento es una tarea.
	var lista []Tarea
	if json.Unmarshal(datos, &lista) == nil && len(lista) > 0 {
		return Tarea{}, fmt.Errorf("el archivo %q contiene %d tareas; usa task_source.tipo=queue o una tarea por archivo", f.ruta, len(lista))
	}
	return Tarea{Descripcion: texto, Origen: "file:" + f.ruta}, nil
}

// --- cola de directorio -----------------------------------------------------

// fuenteCola toma archivos de un directorio en orden alfabético y los consume
// (los mueve a .hecho) para no repetir trabajo tras un reinicio.
type fuenteCola struct {
	dir       string
	intervalo time.Duration
	log       *logx.Logger
	pendiente []string
}

func (f *fuenteCola) Descripcion() string { return "queue:" + f.dir }
func (f *fuenteCola) Cerrar() error       { return nil }

func (f *fuenteCola) Siguiente(ctx context.Context) (Tarea, error) {
	for {
		if len(f.pendiente) == 0 {
			lista, err := f.listar()
			if err != nil {
				return Tarea{}, err
			}
			f.pendiente = lista
		}

		if len(f.pendiente) > 0 {
			ruta := f.pendiente[0]
			f.pendiente = f.pendiente[1:]

			datos, err := os.ReadFile(ruta)
			if err != nil {
				f.log.Warn("no se pudo leer el archivo de la cola", "ruta", ruta, "error", err)
				continue
			}
			tarea := Tarea{Descripcion: strings.TrimSpace(string(datos)), Origen: ruta}
			if err := os.Rename(ruta, ruta+".hecho"); err != nil {
				f.log.Warn("no se pudo marcar el archivo como hecho", "ruta", ruta, "error", err)
			}
			return tarea, nil
		}

		// Sin trabajo ahora mismo: se espera el intervalo de sondeo.
		espera := f.intervalo
		if espera <= 0 {
			espera = 30 * time.Second
		}
		f.log.Debug("cola vacía, esperando", "dir", f.dir, "espera", espera.String())
		select {
		case <-ctx.Done():
			return Tarea{}, io.EOF
		case <-time.After(espera):
		}
	}
}

func (f *fuenteCola) listar() ([]string, error) {
	entradas, err := os.ReadDir(f.dir)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer el directorio de la cola %q: %w", f.dir, err)
	}
	var pendientes []string
	for _, e := range entradas {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".hecho") || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		pendientes = append(pendientes, filepath.Join(f.dir, e.Name()))
	}
	sort.Strings(pendientes)
	return pendientes, nil
}

// --- API --------------------------------------------------------------------

type fuenteAPI struct {
	cfg  config.TaskSource
	http *http.Client
	log  *logx.Logger
}

func newFuenteAPI(cfg config.TaskSource, log *logx.Logger) (*fuenteAPI, error) {
	if cfg.URL == "" {
		return nil, errors.New("task_source.tipo=api requiere url")
	}
	if cfg.Metodo == "" {
		cfg.Metodo = http.MethodGet
	}
	return &fuenteAPI{
		cfg:  cfg,
		http: &http.Client{Timeout: 30 * time.Second},
		log:  log,
	}, nil
}

func (f *fuenteAPI) Descripcion() string { return "api:" + f.cfg.URL }
func (f *fuenteAPI) Cerrar() error       { return nil }

func (f *fuenteAPI) Siguiente(ctx context.Context) (Tarea, error) {
	for {
		tarea, err := f.consultar(ctx)
		if err == nil {
			return tarea, nil
		}
		if errors.Is(err, io.EOF) {
			return Tarea{}, io.EOF
		}
		f.log.Warn("consulta a la API fallida", "url", f.cfg.URL, "error", err)

		espera := f.cfg.Intervalo
		if espera <= 0 {
			espera = 30 * time.Second
		}
		select {
		case <-ctx.Done():
			return Tarea{}, io.EOF
		case <-time.After(espera):
		}
	}
}

func (f *fuenteAPI) consultar(ctx context.Context) (Tarea, error) {
	var cuerpo io.Reader
	if f.cfg.Cuerpo != "" && strings.ToUpper(f.cfg.Metodo) == http.MethodPost {
		cuerpo = bytes.NewReader([]byte(f.cfg.Cuerpo))
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(f.cfg.Metodo), f.cfg.URL, cuerpo)
	if err != nil {
		return Tarea{}, fmt.Errorf("petición inválida: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range f.cfg.Headers {
		req.Header.Set(k, v)
	}

	resp, err := f.http.Do(req)
	if err != nil {
		return Tarea{}, err
	}
	defer resp.Body.Close()

	datos, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return Tarea{}, err
	}
	if resp.StatusCode == http.StatusNoContent {
		return Tarea{}, io.EOF
	}
	if resp.StatusCode != http.StatusOK {
		return Tarea{}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(datos)))
	}

	campo := f.cfg.Campo
	if campo == "" {
		campo = "tarea"
	}

	// Se aceptan tres formas: {"tarea": "..."}, {"tarea": {...}} o texto plano.
	var generico map[string]any
	if json.Unmarshal(datos, &generico) == nil {
		valor, ok := generico[campo]
		if !ok {
			return Tarea{}, fmt.Errorf("la respuesta no trae el campo %q", campo)
		}
		switch v := valor.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				return Tarea{}, errors.New("el campo de la tarea viene vacío")
			}
			return Tarea{Descripcion: v, Origen: f.cfg.URL}, nil
		case map[string]any:
			// {"tarea": {"descripcion": "...", "contexto": {...}}}
			var t Tarea
			crudo, _ := json.Marshal(v)
			if err := json.Unmarshal(crudo, &t); err == nil {
				if t.Descripcion == "" {
					t.Descripcion = strings.TrimSpace(string(crudo))
				}
				t.Origen = f.cfg.URL
				return t, nil
			}
		}
		return Tarea{}, fmt.Errorf("el campo %q tiene un tipo no soportado", campo)
	}

	texto := strings.TrimSpace(string(datos))
	if texto == "" {
		return Tarea{}, errors.New("la API devolvió un cuerpo vacío")
	}
	return Tarea{Descripcion: texto, Origen: f.cfg.URL}, nil
}

// --- Constructores auxiliares (usados por la línea de comandos) -------------

// fuenteTexto entrega una única tarea ya conocida y luego termina.
type fuenteTexto struct {
	tarea Tarea
	hecho bool
}

func (f *fuenteTexto) Descripcion() string { return "texto:" + f.tarea.Origen }
func (f *fuenteTexto) Cerrar() error       { return nil }

func (f *fuenteTexto) Siguiente(ctx context.Context) (Tarea, error) {
	if f.hecho {
		return Tarea{}, io.EOF
	}
	f.hecho = true
	return f.tarea, nil
}

// NuevaTexto construye una fuente de una sola tarea (bandera -tarea).
func NuevaTexto(texto, origen string) (Fuente, error) {
	if strings.TrimSpace(texto) == "" {
		return nil, errors.New("la tarea está vacía")
	}
	return &fuenteTexto{tarea: Tarea{Descripcion: strings.TrimSpace(texto), Origen: origen}}, nil
}

// NuevaArchivo construye una fuente a partir de un archivo (bandera
// -archivo-tarea), sin pasar por la configuración.
func NuevaArchivo(ruta string) (Fuente, error) {
	if strings.TrimSpace(ruta) == "" {
		return nil, errors.New("la ruta del archivo de tarea está vacía")
	}
	return &fuenteArchivo{ruta: ruta}, nil
}
