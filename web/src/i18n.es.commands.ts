// The Spanish text of the choice to approve commands in Settings → Security (ApproveAllSetting.tsx).

const commands: Record<string, string> = {
  "Approving commands": "Aprobar comandos",
  "Allow all": "Permitir todos",
  "Allow all commands": "Permitir todos los comandos",
  "Run every command without asking?":
    "¿Ejecutar todos los comandos sin preguntar?",
  "Motita asks before a command that installs software, uses the network, runs as administrator or touches files outside its workspace.":
    "Motita pregunta antes de un comando que instala software, usa la red, se ejecuta como administrador o toca archivos fuera de su espacio de trabajo.",
  "Motita runs every command without asking. The sandbox still limits what a command can reach, and a few destructive commands (erasing a disk, powering off) stay refused.":
    "Motita ejecuta todos los comandos sin preguntar. El sandbox sigue limitando lo que un comando puede alcanzar, y unos pocos comandos destructivos (borrar un disco, apagar el equipo) siguen rechazados.",
  "Commands you never saw will run on your computer, one after another, with nobody to stop them.":
    "Se ejecutarán en tu computador comandos que nunca viste, uno tras otro, sin nadie que los detenga.",
  "A command shaped by instructions hidden in a web page or a repository would run before you see it.":
    "Un comando influido por instrucciones escondidas en una página web o en un repositorio se ejecutaría antes de que lo veas.",
  "sudo cannot work here: Motita was started with NO_NEW_PRIVS (NoNewPrivs: 1), which no command can take off. To fix it, remove NoNewPrivileges=yes from Motita's systemd unit (or run the container without no-new-privileges) and restart it.":
    "sudo no puede funcionar aquí: Motita se inició con NO_NEW_PRIVS (NoNewPrivs: 1), que ningún comando puede quitar. Para arreglarlo, quita NoNewPrivileges=yes de la unit de systemd de Motita (o ejecuta el contenedor sin no-new-privileges) y reinícialo.",
};

export default commands;
