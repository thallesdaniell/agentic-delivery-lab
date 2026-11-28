# ADR 0003: O agente é o Claude Code headless, um container por run

- **Status:** Aceita
- **Data:** 2026-11-24

## Contexto

O agente precisa ler e editar arquivos, rodar os testes do repositório e devolver um resultado estruturado. Escrever esse loop de tools do zero é trabalho que não é o foco do lab.

## Decisão

O worker roda o Claude Code em modo headless (`-p --bare --output-format json`), com `--json-schema` para o handoff, `--max-budget-usd` para o teto e uma lista fechada de tools: leitura e edição de arquivos, e Bash só para `uv run pytest`, `uv run ruff`, `git status` e `git diff`. Rede, `git push` e `curl` ficam negados. Cada run sobe um container novo, sem bind mount, sem capabilities, com `no-new-privileges` e limite de memória e processos.

## Consequências

- O texto do card vai no prompt entre `<card>` e `</card>`, tratado como dado; o feedback do PR, quando há, entre `<feedback>` e `</feedback>`.
- O worker não guarda estado entre runs: tudo o que importa sai em `result.json` e `changes.patch`.
- Trocar de agente depois exige só outro worker que cumpra o mesmo contrato de saída.
