// The Spanish text of the custom provider and the custom model form in Settings → Models
// (CustomModelSettings.tsx).

const custom: Record<string, string> = {
  "Your own endpoint: URL and token": "tu propio endpoint: URL y token",
  "https://llm.example.com/v1": "https://llm.example.com/v1",
  "Custom model": "Modelo personalizado",
  "Custom model saved": "Modelo personalizado guardado",
  Temperature: "Temperatura",
  "Max tokens": "Máx. de tokens",
  Context: "Contexto",
  "The name your endpoint knows the model by.":
    "El nombre con el que tu endpoint conoce el modelo.",
  "From 0 (precise) to 2 (creative). Empty keeps the endpoint's own.":
    "De 0 (preciso) a 2 (creativo). Vacío deja el del endpoint.",
  "The most tokens one answer may have. Empty keeps the general limit.":
    "Los tokens máximos de una respuesta. Vacío deja el límite general.",
  "The model's window in tokens, so long conversations are summarised in time. Empty asks the model.":
    "La ventana del modelo en tokens, para resumir a tiempo las conversaciones largas. Vacío se lo pregunta al modelo.",
  "e.g. {example}": "p. ej. {example}",
  "Works with any OpenAI-compatible endpoint. Connect it first in Providers (URL and token), then pick it as a model here or per kind of work.":
    "Funciona con cualquier endpoint compatible con OpenAI. Conéctalo primero en Proveedores (URL y token) y luego elígelo como modelo aquí o por tipo de trabajo.",
  "The temperature is a number from 0 to 2.":
    "La temperatura es un número de 0 a 2.",
  "Max tokens and context are whole numbers above zero.":
    "Los tokens máximos y el contexto son números enteros mayores que cero.",
  "Paste the token, e.g. sk-proj-… or your server's token":
    "Pega el token, p. ej. sk-proj-… o el de tu servidor",
};

export default custom;
