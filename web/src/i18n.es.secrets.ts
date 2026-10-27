// The Spanish text of Settings → Secrets (SecretsSettings.tsx).

const secrets: Record<string, string> = {
  Secrets: "Secretos",
  "Variables for the agent": "Variables para el agente",
  "Tokens, keys or URLs the agent can use. Each one reaches every command it runs as an environment variable. The agent is told the names, never the values, and a value is masked in what a command prints.":
    "Tokens, claves o URLs que el agente puede usar. Cada una llega a todos los comandos que ejecuta como variable de entorno. Al agente se le dicen los nombres, nunca los valores, y un valor se oculta en lo que imprime un comando.",
  "Stored in a file only you can read, beside the configuration, and kept out of reach of the agent's commands. Up to {max} variables; saving a name that exists replaces it.":
    "Se guardan en un archivo que solo tú puedes leer, junto a la configuración, fuera del alcance de los comandos del agente. Hasta {max} variables; guardar un nombre que ya existe lo reemplaza.",
  "The gateway runs on its built-in defaults, without a configuration file, so variables cannot be saved here.":
    "El gateway funciona con sus valores por defecto, sin archivo de configuración, así que aquí no se pueden guardar variables.",
  "No variables yet.": "Aún no hay variables.",
  "Variable name": "Nombre de la variable",
  Value: "Valor",
  "Paste the value": "Pega el valor",
  Saved: "Guardada",
  "Saved (…{hint})": "Guardada (…{hint})",
  Remove: "Quitar",
  "Remove {name}": "Quitar {name}",
};

export default secrets;
