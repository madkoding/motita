// Package logx implementa el registro estructurado en JSON con rotación por
// tamaño, sin dependencias externas.
//
// Cada línea es un objeto JSON independiente (JSON Lines), pensado para que lo
// consuma `jq`, un colector de registros o el propio ancla al comprobar
// invariantes.
package logx

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Nivel de severidad.
type Nivel int

const (
	Debug Nivel = iota
	Info
	Warn
	Error
)

func (n Nivel) String() string {
	switch n {
	case Debug:
		return "debug"
	case Info:
		return "info"
	case Warn:
		return "warn"
	case Error:
		return "error"
	default:
		return "desconocido"
	}
}

// ParsearNivel convierte texto ("info") en nivel; error si no se reconoce.
func ParsearNivel(s string) (Nivel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return Debug, nil
	case "info", "":
		return Info, nil
	case "warn", "warning":
		return Warn, nil
	case "error":
		return Error, nil
	default:
		return Info, fmt.Errorf("nivel de registro desconocido %q (usa debug, info, warn o error)", s)
	}
}

// Opciones de construcción de un Logger.
type Opciones struct {
	Ruta    string // archivo destino; vacío = sólo consola
	Nivel   Nivel
	Consola bool
	MaxMB   int // tamaño máximo antes de rotar (0 = sin rotación)
	Backups int // cuántos archivos rotados conservar
}

// Logger escribe registros JSON de forma segura para uso concurrente.
type Logger struct {
	mu      sync.Mutex
	nivel   Nivel
	archivo *os.File
	ruta    string
	escrito int64
	maxByte int64
	backups int
	consola bool
	salida  io.Writer
}

var (
	// global es el registro por defecto, útil cuando no hay uno inyectado.
	global   = &Logger{nivel: Info, consola: true, salida: os.Stderr}
	globalMu sync.RWMutex
)

// Nuevo crea un logger. Si falla la apertura del archivo devuelve el error en
// lugar de caer a un registro silencioso.
func Nuevo(op Opciones) (*Logger, error) {
	l := &Logger{
		nivel:   op.Nivel,
		consola: op.Consola,
		salida:  os.Stderr,
		backups: op.Backups,
	}
	if op.MaxMB > 0 {
		l.maxByte = int64(op.MaxMB) * 1024 * 1024
	}

	if op.Ruta != "" {
		if err := os.MkdirAll(filepath.Dir(op.Ruta), 0o755); err != nil {
			return nil, fmt.Errorf("no se pudo crear el directorio del registro %q: %w", filepath.Dir(op.Ruta), err)
		}
		f, err := os.OpenFile(op.Ruta, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return nil, fmt.Errorf("no se pudo abrir el registro %q: %w", op.Ruta, err)
		}
		l.archivo = f
		l.ruta = op.Ruta
		if info, err := f.Stat(); err == nil {
			l.escrito = info.Size()
		}
	}
	return l, nil
}

// Instalar reemplaza el registro global.
func Instalar(l *Logger) {
	globalMu.Lock()
	global = l
	globalMu.Unlock()
}

// Global devuelve el registro global.
func Global() *Logger {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global
}

// Cerrar cierra el archivo de registro si lo hay.
func (l *Logger) Cerrar() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.archivo == nil {
		return nil
	}
	err := l.archivo.Close()
	l.archivo = nil
	return err
}

// Debug, Info, Warn y Error registran con el nivel correspondiente.
func (l *Logger) Debug(msg string, campos ...any) { l.registrar(Debug, msg, campos...) }
func (l *Logger) Info(msg string, campos ...any)  { l.registrar(Info, msg, campos...) }
func (l *Logger) Warn(msg string, campos ...any)  { l.registrar(Warn, msg, campos...) }
func (l *Logger) Error(msg string, campos ...any) { l.registrar(Error, msg, campos...) }

// registrar arma el objeto JSON y lo escribe en el archivo y/o la consola.
func (l *Logger) registrar(nivel Nivel, msg string, campos ...any) {
	if nivel < l.nivel {
		return
	}

	evento := make(map[string]any, 3+len(campos)/2)
	evento["ts"] = time.Now().Format(time.RFC3339Nano)
	evento["nivel"] = nivel.String()
	evento["msg"] = msg
	for i := 0; i+1 < len(campos); i += 2 {
		clave, ok := campos[i].(string)
		if !ok {
			continue
		}
		evento[clave] = valorJSON(campos[i+1])
	}

	linea, err := json.Marshal(evento)
	if err != nil {
		// Nunca se pierde el mensaje por un campo raro: se degrada a texto plano.
		linea, _ = json.Marshal(map[string]any{
			"ts":    evento["ts"],
			"nivel": nivel.String(),
			"msg":   msg,
			"error": "no se pudo serializar un campo: " + err.Error(),
		})
	}
	linea = append(linea, '\n')

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.archivo != nil {
		n, err := l.archivo.Write(linea)
		if err == nil {
			l.escrito += int64(n)
			l.rotarSiHaceFalta()
		}
	}
	if l.consola && l.salida != nil {
		l.salida.Write(linea)
	}
}

// rotarSiHaceFalta mueve el archivo actual a .1 y desplaza los backups.
func (l *Logger) rotarSiHaceFalta() {
	if l.maxByte <= 0 || l.escrito < l.maxByte || l.archivo == nil {
		return
	}
	l.archivo.Close()
	l.archivo = nil

	if l.backups > 0 {
		// Desplaza .N-1 -> .N de mayor a menor para no pisar nada.
		for i := l.backups - 1; i >= 1; i-- {
			viejo := fmt.Sprintf("%s.%d", l.ruta, i)
			nuevo := fmt.Sprintf("%s.%d", l.ruta, i+1)
			os.Rename(viejo, nuevo)
		}
		os.Rename(l.ruta, l.ruta+".1")
	} else {
		os.Remove(l.ruta)
	}

	f, err := os.OpenFile(l.ruta, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0o644)
	if err != nil {
		// Sin archivo seguimos con la consola: el agente no debe morir por el log.
		l.archivo = nil
		return
	}
	l.archivo = f
	l.escrito = 0
}

// ArchivosRotados lista los backups existentes (para diagnósticos y pruebas).
func (l *Logger) ArchivosRotados() []string {
	if l.ruta == "" {
		return nil
	}
	var salida []string
	for i := 1; i <= l.backups; i++ {
		ruta := fmt.Sprintf("%s.%d", l.ruta, i)
		if _, err := os.Stat(ruta); err == nil {
			salida = append(salida, ruta)
		}
	}
	sort.Strings(salida)
	return salida
}

// valorJSON convierte tipos que no son serializables directamente (errores).
func valorJSON(v any) any {
	switch t := v.(type) {
	case error:
		if t == nil {
			return nil
		}
		return t.Error()
	case time.Duration:
		return t.String()
	default:
		return v
	}
}

// --- Atajos sobre el registro global ---------------------------------------

func Debugf(msg string, campos ...any) { Global().Debug(msg, campos...) }
func Infof(msg string, campos ...any)  { Global().Info(msg, campos...) }
func Warnf(msg string, campos ...any)  { Global().Warn(msg, campos...) }
func Errorf(msg string, campos ...any) { Global().Error(msg, campos...) }
