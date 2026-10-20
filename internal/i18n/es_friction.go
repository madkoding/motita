package i18n

// The friction journal's weekly summary (internal/friction/summary.go) in Spanish. Keys are the
// English text exactly as the package writes it; a format keeps its verbs, in the same order.
var esFriction = map[string]string{
	"Nothing got in the way in the last %d days.":           "En los últimos %d días nada se interpuso.",
	"In the last %d days, this got in the way (%d in all):": "En los últimos %d días, esto se interpuso (%d en total):",
	"Runs that ended with a failure: %d":                    "Ejecuciones que terminaron con un fallo: %d",
	"Steps that had to be tried again: %d":                  "Pasos que hubo que intentar de nuevo: %d",
	"Actions that were refused: %d":                         "Acciones que fueron rechazadas: %d",
	"%s (refused: nobody was there to ask)":                 "%s (rechazada: no había nadie a quien preguntar)",
	"%s (you refused it)":                                   "%s (la rechazaste)",
	"Answers marked as not good: %d":                        "Respuestas marcadas como no buenas: %d",
	"Came up more than once:":                               "Se repitió más de una vez:",
	"Answers you marked as not good:":                       "Respuestas que marcaste como no buenas:",
	"failure":                                               "fallo",
	"retry":                                                 "reintento",
	"refused action":                                        "acción rechazada",
	"(no comment)":                                          "(sin comentario)",
	"%s — about: %s":                                        "%s — sobre: %s",
}

func init() { Register(ES, esFriction) }
