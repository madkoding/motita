// The Spanish text of the pinned plan bar (PinnedPlan.tsx), the line a finished plan leaves, and
// the choice of how a task runs (DirectSetting.tsx).

const pinnedPlan: Record<string, string> = {
  "Proposed plan": "Plan propuesto",
  Approve: "Aprobar",
  "A plan the agent proposed. It becomes active when you approve it: say go ahead, or open it and approve it.":
    "Un plan que propuso el agente. Pasa a activo cuando lo apruebas: di «dale» o ábrelo y apruébalo.",
  "The plan the agent follows until its work is done. Open it to change or drop it.":
    "El plan que el agente sigue hasta terminar su trabajo. Ábrelo para cambiarlo o descartarlo.",
  "plan done: it is no longer pinned": "plan cumplido: ya no está fijado",
  // DirectSetting.tsx
  "How a task runs": "Cómo se ejecuta una tarea",
  "In phases": "Por fases",
  Direct: "Directo",
  "A task is one conversation with native tools (a shell, write and edit files, memory), like Claude Code: no analysis or plan before it, no summary after it. The sandbox and the approvals are the same, and the project's checks run once at the end.":
    "Una tarea es una sola conversación con herramientas nativas (shell, escribir y editar archivos, memoria), como Claude Code: sin análisis ni plan antes, sin resumen después. El sandbox y las aprobaciones son los mismos, y las verificaciones del proyecto corren una vez al final.",
  "A task is analysed and planned first, carried out in rounds the agent reports on, checked at every claim of done, and summarised at the end.":
    "Una tarea se analiza y planifica primero, se ejecuta en rondas que el agente reporta, se verifica cada vez que dice terminar y se resume al final.",
  // TurnTimings.tsx
  "model {time} ({n} calls)": "modelo {time} ({n} llamadas)",
  "commands {time} ({n})": "comandos {time} ({n})",
  "waiting for you {time} ({n})": "esperándote {time} ({n})",
  "checks {time}": "verificaciones {time}",
  "{n} approved earlier in the turn": "{n} aprobados antes en el turno",
  "Where this turn's time went": "En qué se fue el tiempo de este turno",
};

export default pinnedPlan;
