# Imagem única do lab. O mesmo binário `adl` roda em papéis separados:
#   adl orchestrator -> tira runs da fila, sobe um container de worker por run e publica o PR (tem a chave do App)
#   adl worker       -> roda o agente (Claude Code) só com a chave da Anthropic; não recebe credencial do GitHub
#   adl board        -> board web; só fala com o Postgres

FROM golang:1.24-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/adl ./cmd/adl

FROM ghcr.io/astral-sh/uv:0.9.5 AS uv

FROM node:22-bookworm-slim AS runtime
ARG CLAUDE_CODE_VERSION=2.1.330
RUN apt-get update \
 && apt-get install -y --no-install-recommends git ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && npm install -g "@anthropic-ai/claude-code@${CLAUDE_CODE_VERSION}" \
 && npm cache clean --force
COPY --from=uv /uv /uvx /usr/local/bin/
RUN useradd --create-home --uid 10001 agent \
 && mkdir -p /out && chown agent:agent /out
USER agent
WORKDIR /home/agent
ENV UV_PYTHON_INSTALL_DIR=/home/agent/.uv-python \
    DISABLE_AUTOUPDATER=1 \
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

FROM runtime AS final
COPY skills /lab/skills
COPY agents /lab/agents
COPY --from=build /out/adl /usr/local/bin/adl
ENTRYPOINT ["adl"]
