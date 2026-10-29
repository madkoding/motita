// The Spanish text of the gateway's voice service in Settings → Read aloud (SpeechService.tsx).

const speechService: Record<string, string> = {
  "Voice service": "Servicio de voz",
  "Not available from here: this gateway has no configuration file to save in, or is too old to configure it. Set voice.tts in its configuration file, or update the gateway.":
    "No disponible desde aquí: este gateway no tiene archivo de configuración donde guardar, o es muy viejo para configurarlo. Define voice.tts en su archivo de configuración, o actualiza el gateway.",
  "MOTITA_VOICE_TTS_ENABLED is set in the gateway's environment and wins over this switch. Remove it there to turn the voice service on or off from here.":
    "MOTITA_VOICE_TTS_ENABLED está definida en el entorno del gateway y manda sobre este interruptor. Quítala allí para activar o desactivar el servicio de voz desde aquí.",
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
  "Find one on this computer": "Buscar uno en este equipo",
  "No speech server found on the gateway's computer.":
    "No hay ningún servidor de voz en el equipo del gateway.",
};

export default speechService;
