package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Parser YAML mínimo, escrito a mano.
//
// El requisito es "sin dependencias externas más allá de libc/libcurl", así que
// en Go eso significa sólo biblioteca estándar: no se puede usar gopkg.in/yaml.
// Este parser cubre el subconjunto que necesitan los archivos de configuración
// del agente:
//
//   - mapas anidados por indentación (espacios, nunca tabuladores)
//   - secuencias con "- " y listas en línea [a, b, c]
//   - mapas en línea {a: 1, b: 2}
//   - escalares simples, entre comillas simples o dobles
//   - booleanos (true/false/yes/no/on/off), enteros, flotantes y null (~)
//   - escalares de bloque | y > (con los modificadores - y +) para prompts
//   - comentarios con #
//
// Lo que NO soporta se rechaza con un mensaje explícito (anclas, alias, tags,
// documentos múltiples, claves complejas), en lugar de adivinar en silencio.
// ---------------------------------------------------------------------------

type yLinea struct {
	indent int
	texto  string // sin el sangrado
	num    int    // número de línea para los mensajes de error
}

type yParser struct {
	lineas []yLinea
	pos    int
}

// ParseYAML convierte un documento YAML en mapas, secuencias y escalares.
func ParseYAML(datos []byte) (map[string]any, error) {
	p := &yParser{lineas: make([]yLinea, 0, 64)}

	for i, cruda := range strings.Split(strings.ReplaceAll(string(datos), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(cruda, "\t") {
			return nil, fmt.Errorf("línea %d: la indentación no puede usar tabuladores, usa espacios", i+1)
		}
		indent := 0
		for indent < len(cruda) && cruda[indent] == ' ' {
			indent++
		}
		p.lineas = append(p.lineas, yLinea{indent: indent, texto: cruda[indent:], num: i + 1})
	}

	p.saltarVacias()
	if p.fin() {
		return map[string]any{}, nil
	}

	valor, err := p.parseBloque(p.actual().indent)
	if err != nil {
		return nil, err
	}
	p.saltarVacias()
	if !p.fin() {
		return nil, p.errorf("contenido inesperado: %q", p.actual().texto)
	}

	mapa, ok := valor.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("la raíz de la configuración debe ser un mapa (clave: valor), no una secuencia")
	}
	return mapa, nil
}

// --- utilidades de posición -------------------------------------------------

func (p *yParser) fin() bool      { return p.pos >= len(p.lineas) }
func (p *yParser) actual() yLinea { return p.lineas[p.pos] }
func (p *yParser) avanzar()       { p.pos++ }

func (p *yParser) errorf(formato string, args ...any) error {
	if p.fin() {
		return fmt.Errorf("fin del documento: "+formato, args...)
	}
	return fmt.Errorf("línea %d: "+formato, append([]any{p.actual().num}, args...)...)
}

// saltarVacias deja el cursor en la siguiente línea con contenido real: salta
// tanto las líneas en blanco como los comentarios completos. Sin esto, un
// archivo con cabecera de comentarios (lo normal en documentación) fallaba con
// "se esperaba 'clave: valor'".
//
// Ojo: no se usa dentro de los escalares de bloque, donde una línea que empieza
// por # es contenido legítimo.
func (p *yParser) saltarVacias() {
	for !p.fin() {
		texto := p.lineas[p.pos].texto
		if strings.TrimSpace(texto) == "" {
			p.pos++
			continue
		}
		if strings.TrimSpace(recortarComentario(texto)) == "" {
			p.pos++
			continue
		}
		return
	}
}

// --- estructura -------------------------------------------------------------

// parseBloque interpreta la siguiente estructura con la indentación indicada.
func (p *yParser) parseBloque(indent int) (any, error) {
	if p.fin() || p.actual().indent < indent {
		return nil, nil
	}
	if esItem(p.actual().texto) {
		return p.parseSecuencia(indent)
	}
	return p.parseMapa(indent)
}

func (p *yParser) parseMapa(indent int) (map[string]any, error) {
	return p.parseMapaConPrimera(indent, "", "")
}

// parseMapaConPrimera permite arrancar el mapa con un par clave/valor ya leído
// (el texto que sigue a "- " en una secuencia).
func (p *yParser) parseMapaConPrimera(indent int, clavePrimera, restoPrimera string) (map[string]any, error) {
	mapa := map[string]any{}

	procesar := func(clave, resto string, linea int) error {
		switch {
		case resto == "":
			// Puede ser un bloque anidado, una secuencia al mismo nivel o null.
			p.saltarVacias()
			if p.fin() {
				mapa[clave] = nil
				return nil
			}
			siguiente := p.actual()
			if siguiente.indent > indent {
				valor, err := p.parseBloque(siguiente.indent)
				if err != nil {
					return err
				}
				mapa[clave] = valor
				return nil
			}
			if siguiente.indent == indent && esItem(siguiente.texto) {
				valor, err := p.parseSecuencia(indent)
				if err != nil {
					return err
				}
				mapa[clave] = valor
				return nil
			}
			mapa[clave] = nil
			return nil

		case esIndicadorBloque(resto):
			texto, err := p.leerBloqueEscalar(indent, resto)
			if err != nil {
				return err
			}
			mapa[clave] = texto
			return nil

		default:
			valor, err := parseEscalar(resto, linea)
			if err != nil {
				return err
			}
			mapa[clave] = valor
			return nil
		}
	}

	if clavePrimera != "" || restoPrimera != "" {
		if err := procesar(clavePrimera, restoPrimera, 0); err != nil {
			return nil, err
		}
	}

	for {
		p.saltarVacias()
		if p.fin() || p.actual().indent != indent {
			break
		}
		linea := p.actual()
		if esItem(linea.texto) {
			break
		}
		clave, resto, err := partirClave(linea.texto, linea.num)
		if err != nil {
			return nil, err
		}
		p.avanzar()
		if err := procesar(clave, resto, linea.num); err != nil {
			return nil, err
		}
	}
	return mapa, nil
}

func (p *yParser) parseSecuencia(indent int) ([]any, error) {
	items := []any{}

	for {
		p.saltarVacias()
		if p.fin() || p.actual().indent != indent || !esItem(p.actual().texto) {
			break
		}
		linea := p.actual()
		resto := strings.TrimSpace(linea.texto[1:])
		resto = recortarComentario(resto)
		p.avanzar()

		if resto == "" {
			// "-" solo: el item es un bloque anidado.
			p.saltarVacias()
			if !p.fin() && p.actual().indent > indent {
				valor, err := p.parseBloque(p.actual().indent)
				if err != nil {
					return nil, err
				}
				items = append(items, valor)
			} else {
				items = append(items, nil)
			}
			continue
		}

		// "- clave: valor" abre un mapa que continúa en las líneas siguientes.
		if clave, valor, err := intentarClave(resto); err == nil {
			indentMapa := indent + 2
			// Si las líneas siguientes están más sangradas, respetamos su nivel.
			if !p.fin() && p.actual().indent > indent {
				indentMapa = p.actual().indent
			}
			mapa, err := p.parseMapaConPrimera(indentMapa, clave, valor)
			if err != nil {
				return nil, err
			}
			items = append(items, mapa)
			continue
		}

		valor, err := parseEscalar(resto, linea.num)
		if err != nil {
			return nil, err
		}
		items = append(items, valor)
	}
	return items, nil
}

// leerBloqueEscalar consume un escalar de bloque (| o >) y sus líneas.
func (p *yParser) leerBloqueEscalar(indentPadre int, indicador string) (string, error) {
	literal := indicador[0] == '|'
	conservar := strings.Contains(indicador, "+")
	soloUnaLinea := strings.Contains(indicador, "-")

	var crudas []yLinea
	for !p.fin() {
		linea := p.actual()
		if strings.TrimSpace(linea.texto) == "" {
			crudas = append(crudas, linea)
			p.avanzar()
			continue
		}
		if linea.indent <= indentPadre {
			break
		}
		crudas = append(crudas, linea)
		p.avanzar()
	}

	// Indentación efectiva = la menor de las líneas con contenido.
	minIndent := -1
	for _, l := range crudas {
		if strings.TrimSpace(l.texto) == "" {
			continue
		}
		if minIndent < 0 || l.indent < minIndent {
			minIndent = l.indent
		}
	}

	var partes []string
	for _, l := range crudas {
		if strings.TrimSpace(l.texto) == "" {
			partes = append(partes, "")
			continue
		}
		crudo := strings.Repeat(" ", l.indent) + l.texto
		if minIndent > 0 && minIndent <= len(crudo) {
			crudo = crudo[minIndent:]
		}
		partes = append(partes, crudo)
	}

	var texto string
	if literal {
		texto = strings.Join(partes, "\n")
	} else {
		// Plegado: las líneas consecutivas con contenido se unen con espacios.
		var sb strings.Builder
		for i, parte := range partes {
			if i > 0 {
				if parte == "" || partes[i-1] == "" {
					sb.WriteString("\n")
				} else {
					sb.WriteString(" ")
				}
			}
			sb.WriteString(parte)
		}
		texto = sb.String()
	}

	// Chomping: por defecto una sola línea final; "-" elimina; "+" conserva.
	texto = strings.TrimRight(texto, "\n")
	switch {
	case soloUnaLinea:
		// sin salto final
	case conservar:
		texto += "\n\n"
	default:
		texto += "\n"
	}
	return texto, nil
}

// --- escalares --------------------------------------------------------------

func esItem(texto string) bool {
	return texto == "-" || strings.HasPrefix(texto, "- ") || strings.HasPrefix(texto, "-\t")
}

func esIndicadorBloque(s string) bool {
	if s == "" {
		return false
	}
	if s[0] != '|' && s[0] != '>' {
		return false
	}
	for _, r := range s[1:] {
		if r != '-' && r != '+' && r != '1' && r != '2' && r != '3' && r != '4' && r != '5' && r != '6' && r != '7' && r != '8' && r != '9' {
			return false
		}
	}
	return true
}

// partirClave separa "clave: valor" respetando las comillas.
func partirClave(texto string, num int) (string, string, error) {
	clave, resto, err := intentarClave(texto)
	if err != nil {
		return "", "", fmt.Errorf("línea %d: %v", num, err)
	}
	return clave, resto, nil
}

func intentarClave(texto string) (string, string, error) {
	if strings.ContainsAny(texto, "&*!") && !strings.Contains(texto, ":") {
		return "", "", fmt.Errorf("estructura YAML no soportada en %q (anclas, alias y tags no están implementados)", texto)
	}

	enComilla := byte(0)
	for i := 0; i < len(texto); i++ {
		c := texto[i]
		switch {
		case enComilla != 0:
			if c == enComilla {
				enComilla = 0
			}
		case c == '\'' || c == '"':
			enComilla = c
		case c == ':':
			// En YAML la clave termina en ": " o ":<fin>".
			if i+1 == len(texto) || texto[i+1] == ' ' {
				clave := strings.TrimSpace(texto[:i])
				resto := strings.TrimSpace(recortarComentario(strings.TrimSpace(texto[i+1:])))
				if clave == "" {
					return "", "", fmt.Errorf("clave vacía en %q", texto)
				}
				if clave[0] == '\'' || clave[0] == '"' {
					sinComillas, err := descomillar(clave)
					if err != nil {
						return "", "", err
					}
					clave = sinComillas
				}
				return clave, resto, nil
			}
		}
	}
	return "", "", fmt.Errorf("se esperaba 'clave: valor' y se encontró %q", texto)
}

func parseEscalar(texto string, num int) (any, error) {
	texto = strings.TrimSpace(recortarComentario(texto))
	if texto == "" {
		return nil, nil
	}

	if texto[0] == '\'' || texto[0] == '"' {
		return descomillar(texto)
	}
	if texto[0] == '[' {
		return parseSecuenciaEnLinea(texto, num)
	}
	if texto[0] == '{' {
		return parseMapaEnLinea(texto, num)
	}
	if strings.HasPrefix(texto, "&") || strings.HasPrefix(texto, "*") || strings.HasPrefix(texto, "!") {
		return nil, fmt.Errorf("línea %d: anclas, alias y tags no están soportados (%q)", num, texto)
	}

	switch strings.ToLower(texto) {
	case "null", "~":
		return nil, nil
	case "true", "yes", "on":
		return true, nil
	case "false", "no", "off":
		return false, nil
	}
	if entero, err := strconv.ParseInt(texto, 10, 64); err == nil {
		return entero, nil
	}
	if flotante, err := strconv.ParseFloat(texto, 64); err == nil && strings.ContainsAny(texto, ".eE") {
		return flotante, nil
	}
	return texto, nil
}

func descomillar(texto string) (string, error) {
	if len(texto) < 2 {
		return "", fmt.Errorf("comilla sin cerrar en %q", texto)
	}
	comilla := texto[0]
	if texto[len(texto)-1] != comilla {
		return "", fmt.Errorf("comilla sin cerrar en %q", texto)
	}
	cuerpo := texto[1 : len(texto)-1]
	if comilla == '\'' {
		// En comillas simples la única secuencia especial es '' -> '.
		return strings.ReplaceAll(cuerpo, "''", "'"), nil
	}
	var sb strings.Builder
	for i := 0; i < len(cuerpo); i++ {
		if cuerpo[i] == '\\' && i+1 < len(cuerpo) {
			i++
			switch cuerpo[i] {
			case 'n':
				sb.WriteByte('\n')
			case 't':
				sb.WriteByte('\t')
			case 'r':
				sb.WriteByte('\r')
			case '"':
				sb.WriteByte('"')
			case '\\':
				sb.WriteByte('\\')
			default:
				sb.WriteByte('\\')
				sb.WriteByte(cuerpo[i])
			}
			continue
		}
		sb.WriteByte(cuerpo[i])
	}
	return sb.String(), nil
}

func parseSecuenciaEnLinea(texto string, num int) ([]any, error) {
	if !strings.HasSuffix(texto, "]") {
		return nil, fmt.Errorf("línea %d: falta ']' en la lista en línea %q", num, texto)
	}
	cuerpo := strings.TrimSpace(texto[1 : len(texto)-1])
	if cuerpo == "" {
		return []any{}, nil
	}
	partes, err := dividirTop(cuerpo, ',')
	if err != nil {
		return nil, fmt.Errorf("línea %d: %v", num, err)
	}
	salida := make([]any, 0, len(partes))
	for _, parte := range partes {
		valor, err := parseEscalar(parte, num)
		if err != nil {
			return nil, err
		}
		salida = append(salida, valor)
	}
	return salida, nil
}

func parseMapaEnLinea(texto string, num int) (map[string]any, error) {
	if !strings.HasSuffix(texto, "}") {
		return nil, fmt.Errorf("línea %d: falta '}' en el mapa en línea %q", num, texto)
	}
	cuerpo := strings.TrimSpace(texto[1 : len(texto)-1])
	mapa := map[string]any{}
	if cuerpo == "" {
		return mapa, nil
	}
	partes, err := dividirTop(cuerpo, ',')
	if err != nil {
		return nil, fmt.Errorf("línea %d: %v", num, err)
	}
	for _, parte := range partes {
		clave, resto, err := intentarClave(strings.TrimSpace(parte))
		if err != nil {
			return nil, fmt.Errorf("línea %d: %v", num, err)
		}
		valor, err := parseEscalar(resto, num)
		if err != nil {
			return nil, err
		}
		mapa[clave] = valor
	}
	return mapa, nil
}

// dividirTop separa por un separador que esté fuera de comillas y corchetes.
func dividirTop(texto string, sep byte) ([]string, error) {
	var partes []string
	profundidad := 0
	enComilla := byte(0)
	inicio := 0
	for i := 0; i < len(texto); i++ {
		c := texto[i]
		switch {
		case enComilla != 0:
			if c == enComilla {
				enComilla = 0
			}
		case c == '\'' || c == '"':
			enComilla = c
		case c == '[' || c == '{':
			profundidad++
		case c == ']' || c == '}':
			profundidad--
		case c == sep && profundidad == 0:
			partes = append(partes, strings.TrimSpace(texto[inicio:i]))
			inicio = i + 1
		}
	}
	if enComilla != 0 {
		return nil, fmt.Errorf("comilla sin cerrar en %q", texto)
	}
	partes = append(partes, strings.TrimSpace(texto[inicio:]))
	return partes, nil
}

// recortarComentario elimina un "# comentario" que esté fuera de comillas.
func recortarComentario(texto string) string {
	enComilla := byte(0)
	for i := 0; i < len(texto); i++ {
		c := texto[i]
		switch {
		case enComilla != 0:
			if c == enComilla {
				enComilla = 0
			}
		case c == '\'' || c == '"':
			enComilla = c
		case c == '#' && (i == 0 || texto[i-1] == ' '):
			return strings.TrimRight(texto[:i], " ")
		}
	}
	return texto
}
