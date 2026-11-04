// Spanish for a sign-in that stopped working: the expired state of a provider, signing in again
// from the page (claude's own login included) and the chat's way back to it.

const signin: Record<string, string> = {
  "Sign-in expired · sign in again":
    "Sesión caducada · vuelve a iniciar sesión",
  "Its own command's sign-in expired and could not be renewed: sign in again below, or run {cmd} on the gateway's machine as the same user.":
    "La sesión de su propio comando caducó y no se pudo renovar: vuelve a iniciar sesión abajo, o ejecuta {cmd} en la máquina del gateway con el mismo usuario.",
  "The sign-in expired and could not be renewed: sign in again below.":
    "La sesión caducó y no se pudo renovar: vuelve a iniciar sesión abajo.",
  "Sign in on the page, then paste here the code it shows.":
    "Inicia sesión en la página y luego pega aquí el código que muestra.",
  "Code from the sign-in page": "Código de la página de inicio de sesión",
  "The Google login on this computer has expired. Run gcloud auth application-default login there and sign in again, or paste an AI Studio key.":
    "El inicio de sesión de Google de este equipo caducó. Ejecuta gcloud auth application-default login ahí y vuelve a iniciar sesión, o pega una clave de AI Studio.",
  "Reconnect the provider": "Reconectar el proveedor",
};

export default signin;
