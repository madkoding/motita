// The Spanish text of the quality check in Settings → Models (EvalSettings.tsx).

const evals: Record<string, string> = {
  "Quality check": "Revisión de calidad",
  "Runs a few small fixed tasks and tells you if the model got worse than the last time. It uses part of your model's quota, so it only runs when you ask.":
    "Ejecuta unas pocas tareas pequeñas y fijas y te dice si el modelo empeoró respecto a la última vez. Gasta parte de la cuota de tu modelo, así que solo corre cuando lo pides.",
  "Model checked: {model}": "Modelo revisado: {model}",
  "This computer cannot run the check: it needs a command line.":
    "Este equipo no puede hacer la revisión: necesita una línea de comandos.",
  "The last check could not finish: {error}":
    "La última revisión no pudo terminar: {error}",
  "No check yet. Run one to know how the model does today.":
    "Aún no hay revisión. Haz una para saber cómo rinde el modelo hoy.",
  "The model changed since the last check. Run it again to see if it got worse.":
    "El modelo cambió desde la última revisión. Vuelve a hacerla para ver si empeoró.",
  "The instructions the agent works with changed since the last check. Run it again to see if it got worse.":
    "Las instrucciones con las que trabaja el agente cambiaron desde la última revisión. Vuelve a hacerla para ver si empeoró.",
  "The tasks were updated since the last check, so it cannot be compared. Run it again.":
    "Las tareas se actualizaron desde la última revisión, así que no se pueden comparar. Vuelve a hacerla.",
  "First check done. The next one will be compared with this one.":
    "Primera revisión lista. La próxima se comparará con esta.",
  "Better than last time": "Mejor que la última vez",
  "About the same as last time": "Más o menos igual que la última vez",
  "Worse than last time": "Peor que la última vez",
  "Running task {n} of {total}…": "Ejecutando la tarea {n} de {total}…",
  "This runs {n} small tasks on {model} and uses part of your quota. Run it?":
    "Esto ejecuta {n} tareas pequeñas con {model} y gasta parte de tu cuota. ¿Lo hago?",
  "Run the check": "Hacer la revisión",
  "Run check": "Revisar ahora",
  "Check started": "Revisión iniciada",
  "{model} · {passed} of {total} tasks passed":
    "{model} · {passed} de {total} tareas superadas",
  "Steps: {steps}": "Pasos: {steps}",
  "Cost: about {cost}": "Costo: unos {cost}",
  "Cost: unknown for this model": "Costo: desconocido para este modelo",
  "Earlier checks": "Revisiones anteriores",
  "{date} · {model} · {passed} of {total}":
    "{date} · {model} · {passed} de {total}",
  "Stopped working": "Dejó de funcionar",
  Passed: "Superada",
  "Create a file": "Crear un archivo",
  "Fix a typo": "Corregir una errata",
  "Count lines": "Contar líneas",
  "Rename a file": "Renombrar un archivo",
  "Sort lines": "Ordenar líneas",
};

export default evals;
