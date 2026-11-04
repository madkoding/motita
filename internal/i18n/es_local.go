package i18n

// Motita's local model from the terminal (/local, internal/tui/localmodel.go) in Spanish.
var esLocal = map[string]string{
	"the local model on this GPU: install, start, stop, delete": "el modelo local en esta GPU: instalar, " +
		"iniciar, detener, borrar",
	"local: this interface is not attached to a gateway, and the gateway is what runs the local model.": "local: " +
		"esta interfaz no está conectada a un gateway, y es el gateway el que ejecuta el modelo local.",
	"usage: /local [install | cancel | start | stop | delete]": "uso: /local [install | cancel | start | stop | " +
		"delete]",
	"local: this deletes the model and its runtime (%s). Type /local delete confirm to go ahead.": "local: esto " +
		"borra el modelo y su motor (%s). Escribe /local delete confirm para seguir.",
	"local: %s":       "local: %s",
	"Local model: %s": "Modelo local: %s",
	"GPU: none found": "GPU: no se encontró ninguna",
	"GPU: %s · %s":    "GPU: %s · %s",
	"Not available here: the local model needs a GPU with at least 4 GB of memory.": "No disponible aquí: el " +
		"modelo local necesita una GPU con al menos 4 GB de memoria.",
	"Downloading %s: %d%% (/local again shows the progress, /local cancel stops it)": "Descargando %s: %d%% " +
		"(/local de nuevo muestra el avance, /local cancel lo detiene)",
	"the model":               "el modelo",
	"the runtime (llama.cpp)": "el motor (llama.cpp)",
	"Not installed. /local install downloads it (%s).": "No instalado. /local install lo descarga (%s).",
	"Running · %d tokens of context · port %d. Pick it with /models %s; /local stop stops it.": "En marcha · %d " +
		"tokens de contexto · puerto %d. Elígelo con /models %s; /local stop lo detiene.",
	"Loading the model into the GPU… /local again shows when it is ready.": "Cargando el modelo en la GPU… " +
		"/local de nuevo muestra cuándo está listo.",
	"Installed · stopped. /local start starts it.": "Instalado · detenido. /local start lo inicia.",
	"Last download: %s":                            "Última descarga: %s",
	"Server: %s":                                   "Servidor: %s",
	"Deleted.":                                     "Borrado.",
}

func init() { Register(ES, esLocal) }
