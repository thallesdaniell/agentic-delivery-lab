#!/usr/bin/env bash
# Roda um card de ponta a ponta com dois containers:
#   1. worker:  só com a chave da Anthropic; roda o agente e grava o patch e o result.json
#   2. publish: só com a chave do GitHub App, montada só leitura; abre o PR
# Uso: scripts/run-card.sh cards/health.json
set -euo pipefail

cd "$(dirname "$0")/.."
CARD="${1:?uso: scripts/run-card.sh <card.json>}"
[ -f .env ] || { echo "falta o .env (copie de .env.example)" >&2; exit 2; }
set -a; . ./.env; set +a
KEY_PATH="${GITHUB_APP_PRIVATE_KEY_PATH/#\~/$HOME}"

IMAGE="agentic-delivery-lab:dev"
OUT="$(mktemp -d "${TMPDIR:-/tmp}/adl-run.XXXXXX")"
chmod 777 "$OUT"

docker build -q -f deploy/Dockerfile -t "$IMAGE" . >/dev/null

echo "== worker ($(basename "$CARD")) -> $OUT" >&2
set +e
docker run --rm \
  -e ANTHROPIC_API_KEY -e RUN_BUDGET_USD -e RUN_TIMEOUT \
  -v "$PWD/$CARD:/lab/card.json:ro" \
  -v "$PWD/skills:/lab/skills:ro" \
  -v "$PWD/agents:/lab/agents:ro" \
  -v "$OUT:/out" \
  "$IMAGE" worker
WORKER_EXIT=$?
set -e
if [ "$WORKER_EXIT" -ne 0 ]; then
  echo "== worker terminou sem sucesso (exit $WORKER_EXIT); nada foi publicado. Resultado em $OUT/result.json" >&2
  exit "$WORKER_EXIT"
fi

echo "== publish" >&2
docker run --rm \
  -e GITHUB_APP_ID -e GITHUB_APP_INSTALLATION_ID \
  -v "$PWD/$CARD:/lab/card.json:ro" \
  -v "$KEY_PATH:/run/secrets/github-app.pem:ro" \
  -v "$OUT:/out" \
  "$IMAGE" publish
