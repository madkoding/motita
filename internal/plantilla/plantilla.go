// Package plantilla sustituye variables {{nombre}} en los prompts.
//
// Es deliberadamente mínimo: no hay lógica, no hay condicionales y no hay
// evaluación. Un prompt es texto con huecos, y esto sólo rellena los huecos con
// lo que el agente sabe en ese momento. Así el motor de razonamiento nunca
// inventa contexto: o la variable existe y se sustituye, o se avisa.
package plantilla

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var variable = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)

// Render rellena las variables. Las desconocidas se dejan tal cual y se
// devuelven en faltantes, para poder registrarlo en lugar de enviar al LLM un
// prompt con huecos silenciosos.
func Render(texto string, variables map[string]string) (string, []string) {
	faltantes := map[string]bool{}

	salida := variable.ReplaceAllStringFunc(texto, func(m string) string {
		nombre := variable.FindStringSubmatch(m)[1]
		if valor, ok := variables[nombre]; ok {
			return valor
		}
		faltantes[nombre] = true
		return m
	})

	if len(faltantes) == 0 {
		return salida, nil
	}
	lista := make([]string, 0, len(faltantes))
	for nombre := range faltantes {
		lista = append(lista, nombre)
	}
	sort.Strings(lista)
	return salida, lista
}

// Variables lista las variables usadas por un texto (útil para documentar los
// prompts disponibles y para validar la configuración al arrancar).
func Variables(texto string) []string {
	vistas := map[string]bool{}
	for _, m := range variable.FindAllStringSubmatch(texto, -1) {
		vistas[m[1]] = true
	}
	lista := make([]string, 0, len(vistas))
	for nombre := range vistas {
		lista = append(lista, nombre)
	}
	sort.Strings(lista)
	return lista
}

// Concatena un historial de intentos fallidos para el prompt.
func Historial(intentos []string) string {
	if len(intentos) == 0 {
		return "## INTENTOS ANTERIORES\n(ninguno: es el primer intento)"
	}
	var sb strings.Builder
	sb.WriteString("## INTENTOS ANTERIORES QUE FALLARON\n")
	for i, intento := range intentos {
		fmt.Fprintf(&sb, "### Intento fallido %d\n%s\n", i+1, intento)
	}
	return sb.String()
}
