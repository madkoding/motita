// Command mockllm es un LLM simulado para las pruebas de extremo a extremo.
//
// Se comporta como un modelo que primero se equivoca y luego corrige:
//
//	Análisis  -> siempre comprensible, sin subtareas
//	Plan      -> un paso
//	Ejecución -> intento 1: escribe contenido INVÁLIDO (el ancla debe fallar)
//	             intento 2: escribe contenido-válido (el ancla debe pasar)
//
// Está en Go puro y se compila para linux/386, así que corre dentro del mismo
// contenedor de 32 bits que el agente: la prueba no necesita red externa ni
// claves. Implementa los tres dialectos (openai, anthropic, gemini) para poder
// probar cualquiera de ellos.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

var (
	intentosEjecucion int32
)

// fase deduce en qué parte del flujo está el agente a partir del prompt recibido.
func fase(texto string) string {
	switch {
	case strings.Contains(texto, "ANÁLISIS DE LA TAREA") || strings.Contains(texto, "criterios_exito"):
		return "analyze"
	case strings.Contains(texto, "PLAN DE ACCIÓN") || strings.Contains(texto, "resultado_esperado"):
		return "plan"
	case strings.Contains(texto, "## ACCIÓN") || strings.Contains(texto, "razonamiento"):
		return "execute"
	default:
		return "desconocida"
	}
}

// contenidoJSON devuelve el JSON que el "modelo" pondría en la respuesta.
func contenidoJSON(f string, n int) string {
	switch f {
	case "analyze":
		return `{"comprensible":true,"resumen":"dejar el informe con el contenido pedido","criterios_exito":["informe.txt contiene contenido-valido"],"riesgos":[],"necesita_subtareas":false}`
	case "plan":
		return `{"plan":[{"paso":1,"accion":"escribir el informe","comando":"escribir informe.txt"}],"subtareas":[],"resultado_esperado":"informe.txt con contenido-valido"}`
	default:
		// El primer intento de ejecución se equivoca a propósito.
		comando := `printf 'contenido-incorrecto' > informe.txt`
		if n >= 2 {
			comando = `printf 'contenido-valido' > informe.txt`
		}
		salida := map[string]any{
			"razonamiento": fmt.Sprintf("intento %d", n),
			"acciones": []map[string]string{
				{"tipo": "comando", "descripcion": "escribir el informe", "comando": comando},
			},
			"accion_final": map[string]string{"descripcion": "notificar", "comando": ""},
		}
		datos, _ := json.Marshal(salida)
		return string(datos)
	}
}

func responder(w http.ResponseWriter, dialecto, contenido string) {
	w.Header().Set("Content-Type", "application/json")
	switch dialecto {
	case "anthropic":
		json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": contenido}},
		})
	case "gemini":
		json.NewEncoder(w).Encode(map[string]any{
			"candidates": []any{map[string]any{
				"content":      map[string]any{"parts": []map[string]string{{"text": contenido}}},
				"finishReason": "STOP",
			}},
		})
	default:
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]string{"role": "assistant", "content": contenido},
				"finish_reason": "stop",
			}},
		})
	}
}

func manejar(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		fmt.Fprint(w, "ok")
		return
	}

	cuerpo, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	texto := string(cuerpo)
	f := fase(texto)

	dialecto := "openai"
	switch {
	case strings.Contains(r.URL.Path, "messages"):
		dialecto = "anthropic"
	case strings.Contains(r.URL.Path, "generateContent"):
		dialecto = "gemini"
	}

	n := int(atomic.LoadInt32(&intentosEjecucion))
	if f == "execute" {
		n = int(atomic.AddInt32(&intentosEjecucion, 1))
	}
	log.Printf("fase=%s dialecto=%s intento_ejecucion=%d bytes_prompt=%d", f, dialecto, n, len(texto))

	if m := dialogoDePrueba(texto); m != "" {
		// La configuración puede pedir respuestas fijas (no se usa aquí).
		responder(w, dialecto, m)
		return
	}
	responder(w, dialecto, contenidoJSON(f, n))
}

// dialogoDePrueba permite respuestas fijas vía el prompt, usado por otras
// pruebas del repositorio.
func dialogoDePrueba(texto string) string {
	const marca = "RESPONDER_CON:"
	if i := strings.Index(texto, marca); i >= 0 {
		return strings.TrimSpace(texto[i+len(marca):])
	}
	return ""
}

func main() {
	puerto := flag.Int("puerto", 8210, "puerto de escucha")
	host := flag.String("host", "0.0.0.0", "interfaz")
	flag.Parse()

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", *host, *puerto),
		Handler:           http.HandlerFunc(manejar),
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "mockllm escuchando en http://%s:%d/v1\n", *host, *puerto)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
