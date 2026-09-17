#!/usr/bin/env bash
# Prueba de extremo a extremo del binario i386.
#
# Ejecuta el binario compilado para linux/386 dentro de un contenedor de 32 bits
# real, contra un servidor compatible con OpenAI (tools/mockapi) que también
# corre ahí dentro. Verifica que el bucle de agente funciona: el "modelo" pide
# herramientas, starlight las ejecuta en el sistema local, devuelve los
# resultados y recibe la respuesta final.
#
# Uso:  ./scripts/e2e-i386.sh
set -euo pipefail

cd "$(dirname "$0")/.."
IMAGEN="${IMAGEN:-i386/debian:bookworm-slim}"
PUERTO="${PUERTO:-8099}"
MARCA="RESULTADO-E2E"

echo "==> Compilando binarios para linux/386"
make 386 >/dev/null
mkdir -p dist
GOOS=linux GOARCH=386 CGO_ENABLED=0 go build -trimpath -o dist/mockapi-linux-386 ./tools/mockapi
clase="$(head -c 5 dist/starlight-linux-386 | od -An -tx1 | tr -d ' \n')"
[ "$clase" = "7f454c4601" ] || { echo "ERROR: starlight-linux-386 no es ELFCLASS32"; exit 1; }
echo "    ELFCLASS32 confirmado ($clase)"

echo "==> Ejecutando dentro de $IMAGEN (--platform linux/386)"
salida="$(
  docker run --rm --platform linux/386 -v "$PWD/dist:/t:ro" "$IMAGEN" sh -c "
    set -e
    echo \"arquitectura: \$(dpkg --print-architecture)\"
    /t/mockapi-linux-386 -puerto $PUERTO >/tmp/mock.log 2>&1 &
    # Espera activa a que el mock acepte peticiones.
    i=0
    while [ \$i -lt 50 ]; do
      if /t/starlight-linux-386 --version >/dev/null 2>&1 && \
         (exec 3<>/dev/tcp/127.0.0.1/$PUERTO) 2>/dev/null; then break; fi
      i=\$((i+1)); sleep 0.2
    done
    OPENAI_API_KEY=prueba \
    OPENAI_BASE_URL=http://127.0.0.1:$PUERTO/v1 \
    OPENAI_MODEL=mock \
    NO_COLOR=1 \
    /t/starlight-linux-386 -p 'dime la arquitectura y la version del sistema'
    echo '--- peticiones recibidas por el mock ---'
    cat /tmp/mock.log
  "
)"
echo "$salida"

echo
if grep -q "$MARCA" <<<"$salida" && grep -q "arquitectura: i386" <<<"$salida" && grep -q "i386" <<<"$salida"; then
  echo "✅ E2E i386 OK: el binario de 32 bits ejecutó herramientas reales y cerró el bucle de agente."
else
  echo "❌ E2E i386 FALLÓ: no se encontró la marca '$MARCA' o la arquitectura esperada."
  exit 1
fi
