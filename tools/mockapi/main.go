// Command mockapi es un servidor mínimo compatible con la API de OpenAI para
// verificar starlight sin gastar tokens ni necesitar una clave.
//
// Está escrito en Go puro y se compila también para linux/386, de modo que
// puede ejecutarse DENTRO de la misma máquina de 32 bits que starlight: así la
// prueba de extremo a extremo es autocontenida (sin depender de la red del
// anfitrión).
//
// Guion que implementa:
//
//  1. Ante un mensaje de usuario responde con dos llamadas a herramienta:
//     ejecutar_comando ("arch") y leer_archivo ("/etc/os-release").
//  2. Ante los resultados de herramienta devuelve un texto final que los cita,
//     precedido de la marca RESULTADO-E2E.
//
// Uso:
//
//	mockapi -puerto 8099 &
//	export OPENAI_API_KEY=prueba
//	export OPENAI_BASE_URL=http://127.0.0.1:8099/v1
//	starlight -p "dime la arquitectura"
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
	"time"
)

type mensaje struct {
	Role       string `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id"`
	Name       string `json:"name"`
}

type peticion struct {
	Model    string    `json:"model"`
	Messages []mensaje `json:"messages"`
	Tools    []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
	ToolChoice string `json:"tool_choice"`
}

// llamadaHerramienta construye una tool_call con `arguments` como cadena JSON,
// que es exactamente el formato que manda la especificación de OpenAI (y el que
// históricamente rompía a los clientes que esperaban un objeto).
func llamadaHerramienta(id, nombre string, argumentos map[string]string) map[string]any {
	crudo, _ := json.Marshal(argumentos)
	return map[string]any{
		"id":   id,
		"type": "function",
		"function": map[string]any{
			"name":      nombre,
			"arguments": string(crudo),
		},
	}
}

func respuestaToolCalls() map[string]any {
	return map[string]any{
		"id":     "chatcmpl-mock-1",
		"object": "chat.completion",
		"model":  "mock",
		"choices": []map[string]any{{
			"index":         0,
			"finish_reason": "tool_calls",
			"message": map[string]any{
				"role":    "assistant",
				"content": nil,
				"tool_calls": []map[string]any{
					llamadaHerramienta("call_1", "ejecutar_comando", map[string]string{"cmd": "arch; getconf LONG_BIT"}),
					llamadaHerramienta("call_2", "leer_archivo", map[string]string{"ruta": "/etc/os-release"}),
				},
			},
		}},
	}
}

func respuestaFinal(resultados []mensaje) map[string]any {
	partes := make([]string, 0, len(resultados))
	for _, r := range resultados {
		partes = append(partes, fmt.Sprintf("%s=%s", r.Name, strings.Join(strings.Fields(r.Content), " ")))
	}
	return map[string]any{
		"id":     "chatcmpl-mock-2",
		"object": "chat.completion",
		"model":  "mock",
		"choices": []map[string]any{{
			"index":         0,
			"finish_reason": "stop",
			"message": map[string]any{
				"role":    "assistant",
				"content": "RESULTADO-E2E " + strings.Join(partes, " | "),
			},
		}},
	}
}

func manejar(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "ok")
		return
	}
	if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.Error(w, "ruta desconocida: "+r.URL.Path, http.StatusNotFound)
		return
	}

	cuerpo, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		http.Error(w, "petición ilegible", http.StatusBadRequest)
		return
	}
	var pet peticion
	if err := json.Unmarshal(cuerpo, &pet); err != nil {
		http.Error(w, "json inválido", http.StatusBadRequest)
		return
	}

	nombres := make([]string, 0, len(pet.Tools))
	for _, t := range pet.Tools {
		nombres = append(nombres, t.Function.Name)
	}
	ultimo := "ninguno"
	if len(pet.Messages) > 0 {
		ultimo = pet.Messages[len(pet.Messages)-1].Role
	}
	log.Printf("petición: mensajes=%d ultimo_rol=%s tool_choice=%q herramientas=%v",
		len(pet.Messages), ultimo, pet.ToolChoice, nombres)

	var salida map[string]any
	if ultimo == "tool" {
		var resultados []mensaje
		for _, m := range pet.Messages {
			if m.Role == "tool" {
				resultados = append(resultados, m)
			}
		}
		salida = respuestaFinal(resultados)
	} else {
		salida = respuestaToolCalls()
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(salida)
}

func main() {
	puerto := flag.Int("puerto", 8099, "puerto de escucha")
	host := flag.String("host", "0.0.0.0", "interfaz de escucha")
	flag.Parse()

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", *host, *puerto),
		Handler:           http.HandlerFunc(manejar),
		ReadHeaderTimeout: 10 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "mockapi escuchando en http://%s:%d/v1\n", *host, *puerto)
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
