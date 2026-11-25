// The Spanish text of Settings → Inference (InferenceSettings.tsx).

const inference: Record<string, string> = {
  Inference: "Inferencia",
  "Inference servers": "Servidores de inferencia",
  "Servers that run models for you: llama.cpp, LM Studio, Ollama or any OpenAI-compatible endpoint. Keys stay in a file only you can read and are never shown again.":
    "Servidores que ejecutan modelos por ti: llama.cpp, LM Studio, Ollama o cualquier endpoint compatible con OpenAI. Las claves quedan en un archivo que solo tú puedes leer y no se vuelven a mostrar.",
  "No servers yet.": "Aún no hay servidores.",
  "Server name": "Nombre del servidor",
  "My GPU box": "Mi equipo con GPU",
  Kind: "Tipo",
  Address: "Dirección",
  "Model (optional)": "Modelo (opcional)",
  "Key (optional)": "Clave (opcional)",
  "Leave empty to keep the saved key":
    "Déjalo vacío para conservar la clave guardada",
  Add: "Agregar",
  Edit: "Editar",
  Cancel: "Cancelar",
  Test: "Probar",
  Use: "Usar",
  "Use {name}": "Usar {name}",
  "In use": "En uso",
  "Managed in Settings → Providers": "Se gestiona en Ajustes → Proveedores",
  Testing: "Probando",
  "Key saved (…{hint})": "Clave guardada (…{hint})",
  "Key saved": "Clave guardada",
  "No key": "Sin clave",
  "{count} models in {ms} ms": "{count} modelos en {ms} ms",
  "Not reachable: {error}": "Sin respuesta: {error}",
  "Remove {name}": "Quitar {name}",
  "Edit {name}": "Editar {name}",
  "Test {name}": "Probar {name}",
  "Up to {max} servers.": "Hasta {max} servidores.",
  "The gateway runs on its built-in defaults, without a configuration file, so servers cannot be saved here.":
    "El gateway funciona con sus valores por defecto, sin archivo de configuración, así que aquí no se pueden guardar servidores.",
};

export default inference;
