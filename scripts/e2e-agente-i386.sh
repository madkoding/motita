#!/usr/bin/env bash
# Prueba de extremo a extremo del agente de 3 capas en 32 bits reales.
#
# Dentro de un contenedor linux/386:
#   1. se levanta un LLM simulado (tools/mockllm) que se comporta como un modelo
#      que primero falla y luego corrige,
#   2. se ejecuta el agente con una configuración real,
#   3. el ANCLA valida con un comando de verdad (existe el archivo y tiene el
#      contenido correcto),
#   4. si valida, se ejecuta la ACCIÓN FINAL.
#
# Se comprueba el resultado en el sistema de archivos, no lo que dice el agente.
#
# Uso:  ./scripts/e2e-agente-i386.sh
set -euo pipefail

cd "$(dirname "$0")/.."
IMAGEN="${IMAGEN:-i386/debian:bookworm-slim}"
PUERTO="${PUERTO:-8210}"

echo "==> Compilando para linux/386 (agente + LLM simulado)"
make agent-386 >/dev/null
mkdir -p dist
GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -trimpath -o dist/mockllm-linux-386 ./tools/mockllm
clase="$(head -c 5 dist/starlight-agent-386 | od -An -tx1 | tr -d ' \n')"
[ "$clase" = "7f454c4601" ] || { echo "ERROR: el agente no es ELFCLASS32"; exit 1; }
echo "    ELFCLASS32 confirmado ($clase)"

echo "==> Preparando el escenario"
rm -rf .e2e && mkdir -p .e2e/trabajo
cp configs/e2e-agente.yaml .e2e/config.yaml
cp configs/e2e-tarea.txt .e2e/tarea.txt

salida="$(
  docker run --rm --platform linux/386 \
    -v "$PWD/dist:/dist:ro" \
    -v "$PWD/.e2e:/e2e" \
    -w /e2e \
    "$IMAGEN" sh -c "
    set -e
    echo \"arquitectura: \$(dpkg --print-architecture)\"
    /dist/mockllm-linux-386 -puerto $PUERTO >/e2e/mock.log 2>&1 &
    i=0
    while [ \$i -lt 50 ]; do
      (exec 3<>/dev/tcp/127.0.0.1/$PUERTO) 2>/dev/null && break
      i=\$((i+1)); sleep 0.2
    done
    NO_COLOR=1 STARLIGHT_LLM_API_KEY=prueba \
      /dist/starlight-agent-386 -config /e2e/config.yaml 2>&1 || echo \"[el agente salió con \$?]\"
    echo '--- peticiones recibidas por el LLM simulado ---'
    cat /e2e/mock.log
  "
)"
echo "$salida"
echo

echo "==> Comprobación del resultado en el sistema de archivos"
fallos=0
comprobar() {
  if [ -e "$1" ]; then echo "  ✓ $2"; else echo "  ✗ FALTA: $2 ($1)"; fallos=$((fallos+1)); fi
}
comprobar .e2e/trabajo/informe.txt "el comando del intento final creó el informe"
comprobar .e2e/trabajo/accion-final.txt "la acción final se ejecutó tras el PASS"

if [ -f .e2e/trabajo/informe.txt ]; then
  contenido="$(cat .e2e/trabajo/informe.txt)"
  if [ "$contenido" = "contenido-valido" ]; then
    echo "  ✓ el informe tiene el contenido pedido: $contenido"
  else
    echo "  ✗ el informe tiene contenido inesperado: $contenido"
    fallos=$((fallos+1))
  fi
fi

# El ancla debe haber fallado en el primer intento y pasado en el segundo: eso
# demuestra que el bucle de reintento funciona con datos reales.
if grep -q "intento fallido" .e2e/registro.log 2>/dev/null || echo "$salida" | grep -q "intento fallido"; then
  echo "  ✓ hubo un intento fallido antes del PASS (el reintento funcionó)"
else
  echo "  ? no se encontró constancia de un intento fallido"
fi

rm -rf .e2e

echo
if [ "$fallos" -eq 0 ]; then
  echo "✅ E2E del agente de 3 capas en i386: OK"
else
  echo "❌ E2E FALLÓ con $fallos comprobación(es)"
  exit 1
fi
