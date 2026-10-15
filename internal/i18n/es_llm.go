package i18n

// The sentences a failed call to an LLM provider is shown with (internal/llm/friendly.go), in
// Spanish. They are swapped inside longer messages, so each one is a whole sentence.
var esLLM = map[string]string{
	"The provider rejected the API key or the login.":      "El proveedor rechazó la clave de API o el inicio de sesión.",
	"The key has no permission for this model or action.":  "La clave no tiene permiso para este modelo o esta acción.",
	"The account is out of credit or over its quota.":      "La cuenta se quedó sin crédito o superó su cuota.",
	"The provider is limiting requests right now.":         "El proveedor está limitando las solicitudes en este momento.",
	"The provider does not have this model.":               "El proveedor no tiene este modelo.",
	"The provider is overloaded.":                          "El proveedor está sobrecargado.",
	"The provider had an internal error.":                  "El proveedor tuvo un error interno.",
	"The conversation is too long for this model.":         "La conversación es demasiado larga para este modelo.",
	"The provider could not accept the request.":           "El proveedor no pudo aceptar la solicitud.",
	"The provider returned an error.":                      "El proveedor devolvió un error.",
	"Could not reach the provider: check your connection.": "No se pudo contactar al proveedor: revisa tu conexión.",
	"The provider took too long to answer.":                "El proveedor tardó demasiado en responder.",
	"Try again in a moment.":                               "Inténtalo de nuevo en un momento.",
}

func init() { Register(ES, esLLM) }
