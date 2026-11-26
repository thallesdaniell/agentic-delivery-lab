---
name: python-feature
description: Implementar uma funcionalidade pequena numa API Python gerenciada com uv, com teste primeiro.
---

# Funcionalidade em API Python (uv, FastAPI, pytest, ruff)

## Como trabalhar

1. Leia o README e a estrutura em `src/` e `tests/` antes de mudar qualquer coisa. Siga o estilo que já existe.
2. Escreva primeiro o teste que descreve o critério de aceite e rode `uv run pytest` para vê-lo falhar.
3. Implemente o mínimo para o teste passar. Não refatore o que o card não pede.
4. Atualize a documentação que o card citar (por exemplo, a tabela de endpoints do README).
5. Rode, nesta ordem, e corrija até passarem:
   - `uv run ruff format .`
   - `uv run ruff check .`
   - `uv run pytest`

## Limites

- As dependências já estão instaladas. Não adicione dependência nova; se o card exigir uma, termine com outcome `blocked` e explique.
- Não altere `.github/`, `pyproject.toml` nem `uv.lock`, salvo pedido explícito do card.
- No handoff, `tests_run` lista cada comando de verificação com o resultado (por exemplo, `uv run pytest: 6 passed`).
