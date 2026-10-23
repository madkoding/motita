// Spanish for auto mode's tag: why a turn ran as a plan or a task. The reasons are the ones the
// gateway writes on the turn's first line ("mode: plan (question)"); see internal/intent.

const automode: Record<string, string> = {
  "Auto mode picked it: {why}": "El modo automático lo eligió: {why}",
  question: "pregunta",
  "reports a problem": "reporta un problema",
  "asks for a plan": "pide un plan",
  "asked not to change anything": "pidió no cambiar nada",
  "carrying out the plan": "ejecuta el plan",
  "go-ahead": "visto bueno",
  model: "modelo",
  default: "por defecto",
  "the model did not decide": "el modelo no decidió",
};

export default automode;
