// The Spanish text of the kinds of memory, the associations between entries and the
// reorganization of the memory (memoryKinds.ts, MemoryFacts.tsx, MemoryReorganize.tsx).

const memoryKinds: Record<string, string> = {
  // memoryKinds.ts
  Facts: "Hechos",
  "What happened": "Lo que pasó",
  Decisions: "Decisiones",
  "Your preferences": "Tus preferencias",
  People: "Personas",
  "How it is done": "Cómo se hace",
  "Project lessons": "Lecciones de proyecto",
  "Not sorted yet": "Sin clasificar todavía",
  "Related to": "Relacionada con",
  Replaces: "Reemplaza a",
  Contradicts: "Contradice a",
  "Depends on": "Depende de",
  "Comes from": "Viene de",
  "Part of": "Parte de",
  "Example of": "Ejemplo de",
  "Keeping a copy of your memory…": "Guardando una copia de tu memoria…",
  "Joining each project's lessons…": "Juntando las lecciones de cada proyecto…",
  "Sorting each entry by kind…": "Clasificando cada entrada por tipo…",
  "Tying related entries together…": "Asociando las entradas relacionadas…",
  "{n} entries sorted by kind": "{n} entradas clasificadas por tipo",
  "{n} entries got a clearer title": "{n} entradas con un título más claro",
  "{n} duplicates merged": "{n} duplicadas fusionadas",
  "{n} newer facts tied to the ones they replace":
    "{n} hechos nuevos asociados a los que reemplazan",
  "{n} contradictions marked": "{n} contradicciones marcadas",
  "{n} new associations": "{n} asociaciones nuevas",
  "{n} project lesson lists moved into their project":
    "{n} listas de lecciones movidas a su proyecto",
  "1 entry sorted by kind": "1 entrada clasificada por tipo",
  "1 entry got a clearer title": "1 entrada con un título más claro",
  "1 duplicate merged": "1 duplicada fusionada",
  "1 newer fact tied to the one it replaces":
    "1 hecho nuevo asociado al que reemplaza",
  "1 contradiction marked": "1 contradicción marcada",
  "1 new association": "1 asociación nueva",
  "1 project lesson list moved into its project":
    "1 lista de lecciones movida a su proyecto",
  // MemoryFacts.tsx
  "Associated with": "Se asocia con",
  "No entry with this name yet": "Todavía no hay una entrada con este nombre",
  "Kind of memory": "Tipo de memoria",
  "Every kind": "Todos los tipos",
  // MemoryLibrary.tsx
  "Reorganize the memory": "Reorganizar la memoria",
  "Sort by kind, merge duplicates and tie related entries, with undo":
    "Clasificar por tipo, fusionar duplicados y asociar lo relacionado, con deshacer",
  // MemoryReorganize.tsx
  "Sorts each entry by kind: facts, what happened, decisions, your preferences, people, how it is done and project lessons.":
    "Clasifica cada entrada por tipo: hechos, lo que pasó, decisiones, tus preferencias, personas, cómo se hace y lecciones de proyecto.",
  "Merges entries that say the same thing, and gives a clear title to the ones without one.":
    "Fusiona las entradas que dicen lo mismo y da un título claro a las que no lo tienen.",
  "Ties a newer fact to the one it replaces, and marks entries that contradict each other.":
    "Asocia un hecho nuevo al que reemplaza y marca las entradas que se contradicen.",
  "Associates entries about the same thing, so the map shows how they relate.":
    "Asocia las entradas sobre lo mismo, para que el mapa muestre cómo se relacionan.",
  "Moves each project's lessons into that project's own collection.":
    "Mueve las lecciones de cada proyecto a la colección de ese proyecto.",
  "A copy of your memory is kept first, so you can undo it all.":
    "Antes se guarda una copia de tu memoria, así que puedes deshacerlo todo.",
  Reorganize: "Reorganizar",
  "{done} of {total}": "{done} de {total}",
  "It stopped before the end. What was done is kept, and Undo takes it back.":
    "Se detuvo antes de terminar. Lo hecho se conserva, y Deshacer lo revierte.",
  "Everything was already in order.": "Todo ya estaba en orden.",
  "No model was available, so only the lessons were moved and close entries tied.":
    "No había un modelo disponible, así que solo se movieron las lecciones y se asociaron las entradas parecidas.",
  "The model did not answer 1 time; those entries stay as they were.":
    "El modelo no respondió 1 vez; esas entradas quedan como estaban.",
  "The model did not answer {n} times; those entries stay as they were.":
    "El modelo no respondió {n} veces; esas entradas quedan como estaban.",
  "{n} entries in your memory now.": "{n} entradas en tu memoria ahora.",
  Undo: "Deshacer",
  // useReorganize.ts
  "could not read how the reorganization goes":
    "no se pudo leer cómo va la reorganización",
  "could not start the reorganization": "no se pudo iniciar la reorganización",
  "could not undo the reorganization": "no se pudo deshacer la reorganización",
};

export default memoryKinds;
