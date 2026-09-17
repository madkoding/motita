package config

// ---------------------------------------------------------------------------
// Plantillas de prompt por defecto.
//
// Están en español, pero son 100% reemplazables desde el YAML: el motor nunca
// escribe texto de prompt por su cuenta, sólo sustituye las variables {{...}}
// de estas plantillas.
// ---------------------------------------------------------------------------

// PlantillaBaseAnalyze pide el análisis de la tarea y la definición de los
// criterios de éxito que el ancla comprobará después.
var PlantillaBaseAnalyze = Plantilla{
	Sistema: `Eres la Capa B (motor de razonamiento) de un agente con validación determinista.
Trabajas en ciclos: propones un resultado, una Capa A independiente lo valida
ejecutando comprobaciones reales, y si falla recibes los registros del fallo.
Responde SIEMPRE en español y en el formato JSON que se te pida, sin texto extra.`,
	Usuario: `## TAREA
{{tarea}}

## CONTEXTO
- Directorio de trabajo: {{workspace}}
- Intento: {{intento}} de {{max_intentos}}

## REGLAS DE VALIDACIÓN QUE SE APLICARÁN
{{reglas}}

## ANÁLISIS DE LA TAREA
Devuelve un JSON con esta forma exacta:
{
  "comprensible": true,
  "resumen": "qué hay que conseguir, en una frase",
  "criterios_exito": ["criterio verificable 1", "criterio verificable 2"],
  "riesgos": ["riesgo o ambigüedad detectada"],
  "necesita_subtareas": false
}
Si la tarea es ambigua o imposible con las herramientas disponibles, marca
"comprensible": false y explica el motivo en "riesgos". No inventes datos.`,
}

// PlantillaBasePlan pide el plan de acción.
var PlantillaBasePlan = Plantilla{
	Sistema: PlantillaBaseAnalyze.Sistema,
	Usuario: `## TAREA
{{tarea}}

## ANÁLISIS PREVIO
{{analisis}}

## PLAN DE ACCIÓN
Devuelve un JSON con esta forma exacta:
{
  "plan": [
    {"paso": 1, "accion": "qué se hace", "comando": "comando de shell exacto o vacío"}
  ],
  "subtareas": ["subtarea independiente, si hace falta dividir"],
  "resultado_esperado": "qué debería verse cuando esté bien hecho"
}
Los comandos deben ser comprobables y no destructivos salvo que la tarea lo
exija de forma explícita. Cada paso, un solo comando.`,
}

// PlantillaBaseExecute pide la acción concreta a ejecutar y, si corresponde, la
// acción final (commit, envío, guardado) que sólo corre tras un PASS.
var PlantillaBaseExecute = Plantilla{
	Sistema: PlantillaBaseAnalyze.Sistema,
	Usuario: `## TAREA
{{tarea}}

## PLAN
{{plan}}

{{historial}}

## ACCIÓN
Devuelve un JSON con esta forma exacta:
{
  "razonamiento": "por qué esta acción cumple la tarea",
  "acciones": [
    {"tipo": "comando", "descripcion": "qué hace", "comando": "comando exacto de shell"}
  ],
  "accion_final": {"descripcion": "commit, envío o guardado previsto", "comando": "comando exacto o vacío"}
}
Reglas:
- "acciones" son los pasos que producen el resultado; se ejecutarán aislados.
- "accion_final" se ejecuta SÓLO si la validación pasa; si no aplica, deja el
  comando en "" y describes por qué.
- Si el intento falló antes, corrige a partir de los registros; no repitas la
  misma acción esperando otro resultado.`,
}
