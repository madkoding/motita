// The Spanish text of Motita's own local model in Settings → Providers (LocalModelSettings.tsx).

const local: Record<string, string> = {
  "free on this computer's GPU (4 GB or more)":
    "gratis en la GPU de este computador (4 GB o más)",
  "Local model": "Modelo local",
  "This GPU has {have} of memory; the local model needs at least {need}.":
    "Esta GPU tiene {have} de memoria; el modelo local necesita al menos {need}.",
  "No GPU with readable memory was found: the local model needs at least 4 GB of GPU memory.":
    "No se encontró una GPU cuya memoria se pueda leer: el modelo local necesita al menos 4 GB de memoria de GPU.",
  "No GPU found": "No se encontró GPU",
  "{name} · {size} of GPU memory": "{name} · {size} de memoria de GPU",
  "Downloading the model…": "Descargando el modelo…",
  "Downloading the runtime (llama.cpp)…": "Descargando el motor (llama.cpp)…",
  "{what} {pct}%": "{what} {pct}%",
  "Running · {ctx} tokens of context · port {port}":
    "En marcha · {ctx} tokens de contexto · puerto {port}",
  "Loading the model into the GPU…": "Cargando el modelo en la GPU…",
  "Installed · stopped": "Instalado · detenido",
  "Runs {model} on this computer's GPU, as one more provider. Nothing is downloaded or started until you press a button.":
    "Ejecuta {model} en la GPU de este computador, como un proveedor más. No se descarga ni se inicia nada hasta que pulses un botón.",
  "Download ({size})": "Descargar ({size})",
  Start: "Iniciar",
  "Delete {size} from this computer": "Borrar {size} de este computador",
  "Saved in {dir}": "Guardado en {dir}",
  "Local model deleted": "Modelo local borrado",
  "the local model on this GPU: install, start, stop, delete":
    "el modelo local en esta GPU: instalar, iniciar, detener, borrar",
};

export default local;
