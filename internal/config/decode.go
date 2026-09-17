package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Decodificación: mapa genérico (salido del parser YAML) -> structs tipados.
//
// Se hace con reflexión para mantener un único lugar donde están las reglas de
// conversión y para que los errores digan exactamente qué campo está mal, en
// lugar de fallar en silencio como haría un "buscar la clave y ya".
// ---------------------------------------------------------------------------

var tipoDuracion = reflect.TypeOf(time.Duration(0))

// Decodificar vuelca un mapa YAML en un struct cuyos campos llevan la etiqueta
// `yaml:"..."`. Los campos ausentes conservan su valor actual (por eso primero
// se cargan los valores por defecto).
func Decodificar(mapa map[string]any, destino any) error {
	v := reflect.ValueOf(destino)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return fmt.Errorf("el destino de la decodificación debe ser un puntero no nulo")
	}
	return decodificarMapa(mapa, v.Elem(), "")
}

func decodificarMapa(mapa map[string]any, destino reflect.Value, prefijo string) error {
	if destino.Kind() != reflect.Struct {
		return fmt.Errorf("%s: se esperaba una estructura y se encontró %s", rutaONombre(prefijo), destino.Kind())
	}

	campos := camposYAML(destino)
	for clave, valor := range mapa {
		campo, ok := campos[clave]
		if !ok {
			return fmt.Errorf("clave desconocida %q%s (revisa el ejemplo configs/agent.yaml.example)", clave, enRuta(prefijo))
		}
		ruta := clave
		if prefijo != "" {
			ruta = prefijo + "." + clave
		}
		if err := asignar(valor, campo, ruta); err != nil {
			return err
		}
	}
	return nil
}

// camposYAML indexa los campos por su etiqueta yaml.
func camposYAML(v reflect.Value) map[string]reflect.Value {
	tipo := v.Type()
	salida := make(map[string]reflect.Value, tipo.NumField())
	for i := 0; i < tipo.NumField(); i++ {
		etiqueta := tipo.Field(i).Tag.Get("yaml")
		if etiqueta == "" || etiqueta == "-" {
			continue
		}
		nombre := strings.Split(etiqueta, ",")[0]
		salida[nombre] = v.Field(i)
	}
	return salida
}

func asignar(valor any, campo reflect.Value, ruta string) error {
	// Un bloque anidado entra como mapa.
	if mapa, ok := valor.(map[string]any); ok {
		switch campo.Kind() {
		case reflect.Struct:
			return decodificarMapa(mapa, campo, ruta)
		case reflect.Map:
			// map[string]string tal como llega del YAML: {clave: valor}.
			if campo.Type().Key().Kind() != reflect.String || campo.Type().Elem().Kind() != reflect.String {
				return fmt.Errorf("%s: sólo se admiten mapas de texto a texto", ruta)
			}
			nuevo := reflect.MakeMap(campo.Type())
			for k, v := range mapa {
				nuevo.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(aTexto(v)))
			}
			campo.Set(nuevo)
			return nil
		default:
			return fmt.Errorf("%s: se esperaba un valor simple y se encontró un bloque anidado", ruta)
		}
	}

	if valor == nil {
		// null explícito: se respeta el valor por defecto del campo.
		return nil
	}

	switch campo.Type() {
	case tipoDuracion:
		d, err := aDuracion(valor, ruta)
		if err != nil {
			return err
		}
		campo.SetInt(int64(d))
		return nil
	}

	switch campo.Kind() {
	case reflect.String:
		campo.SetString(aTexto(valor))

	case reflect.Bool:
		b, ok := valor.(bool)
		if !ok {
			return fmt.Errorf("%s: se esperaba true/false y se encontró %v", ruta, valor)
		}
		campo.SetBool(b)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := aEntero(valor)
		if err != nil {
			return fmt.Errorf("%s: %v", ruta, err)
		}
		if campo.OverflowInt(n) {
			return fmt.Errorf("%s: el valor %d no cabe en %s", ruta, n, campo.Kind())
		}
		campo.SetInt(n)

	case reflect.Float32, reflect.Float64:
		f, err := aFlotante(valor)
		if err != nil {
			return fmt.Errorf("%s: %v", ruta, err)
		}
		campo.SetFloat(f)

	case reflect.Slice:
		items, ok := valor.([]any)
		if !ok {
			return fmt.Errorf("%s: se esperaba una lista y se encontró %v", ruta, valor)
		}
		nueva := reflect.MakeSlice(campo.Type(), 0, len(items))
		for i, item := range items {
			elemento := reflect.New(campo.Type().Elem()).Elem()
			if err := asignar(item, elemento, fmt.Sprintf("%s[%d]", ruta, i)); err != nil {
				return err
			}
			nueva = reflect.Append(nueva, elemento)
		}
		campo.Set(nueva)

	case reflect.Map:
		crudo, ok := valor.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: se esperaba un mapa y se encontró %v", ruta, valor)
		}
		if campo.Type().Key().Kind() != reflect.String || campo.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("%s: sólo se admiten mapas de texto a texto", ruta)
		}
		nuevo := reflect.MakeMap(campo.Type())
		for k, v := range crudo {
			nuevo.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(aTexto(v)))
		}
		campo.Set(nuevo)

	default:
		return fmt.Errorf("%s: tipo de campo no soportado (%s)", ruta, campo.Kind())
	}
	return nil
}

// aDuracion acepta números (segundos) y textos tipo "30s", "5m", "1h30m".
func aDuracion(valor any, ruta string) (time.Duration, error) {
	switch v := valor.(type) {
	case int64:
		return time.Duration(v) * time.Second, nil
	case float64:
		return time.Duration(v * float64(time.Second)), nil
	case string:
		texto := strings.TrimSpace(v)
		if texto == "" {
			return 0, fmt.Errorf("%s: duración vacía", ruta)
		}
		d, err := time.ParseDuration(texto)
		if err != nil {
			// Sin sufijo se interpreta como segundos, que es lo que espera
			// cualquiera que escriba "timeout: 30".
			if segundos, errSeg := strconv.Atoi(texto); errSeg == nil {
				return time.Duration(segundos) * time.Second, nil
			}
			return 0, fmt.Errorf("%s: duración inválida %q (usa 30, \"30s\", \"5m\", \"1h\")", ruta, texto)
		}
		return d, nil
	default:
		return 0, fmt.Errorf("%s: se esperaba una duración y se encontró %v", ruta, valor)
	}
}

func aEntero(valor any) (int64, error) {
	switch v := valor.(type) {
	case int64:
		return v, nil
	case float64:
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("se esperaba un entero y se encontró %v", v)
		}
		return int64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		texto := strings.TrimSpace(v)
		n, err := strconv.ParseInt(texto, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("se esperaba un entero y se encontró %q", v)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("se esperaba un entero y se encontró %v", valor)
	}
}

func aFlotante(valor any) (float64, error) {
	switch v := valor.(type) {
	case int64:
		return float64(v), nil
	case float64:
		return v, nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("se esperaba un número y se encontró %q", v)
		}
		return f, nil
	default:
		return 0, fmt.Errorf("se esperaba un número y se encontró %v", valor)
	}
}

func aTexto(valor any) string {
	switch v := valor.(type) {
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func enRuta(prefijo string) string {
	if prefijo == "" {
		return ""
	}
	return " dentro de " + prefijo
}

func rutaONombre(prefijo string) string {
	if prefijo == "" {
		return "la configuración"
	}
	return prefijo
}
