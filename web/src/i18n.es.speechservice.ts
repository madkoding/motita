// The Spanish text of the gateway's voice service in Settings → Read aloud (SpeechService.tsx).

const speechService: Record<string, string> = {
  "Voice service": "Servicio de voz",
  "Speech service": "Servicio de síntesis",
  "Off. Turn it on to read answers aloud with a voice service.":
    "Desactivado. Actívalo para leer las respuestas en voz alta con un servicio de voz.",
  "Texts go to {endpoint} ({model}).": "Los textos van a {endpoint} ({model}).",
  "Change the voice service?": "¿Cambiar el servicio de voz?",
  "Turn on the voice service?": "¿Activar el servicio de voz?",
  "The text and the audio are not stored, by the gateway or by this page.":
    "El texto y el audio no se guardan, ni en el gateway ni en esta página.",
  "Name a speech service to read aloud with.":
    "Indica un servicio de síntesis para leer en voz alta.",
  "The text of every answer read aloud is sent to {endpoint}.":
    "El texto de cada respuesta leída en voz alta se envía a {endpoint}.",
};

export default speechService;
