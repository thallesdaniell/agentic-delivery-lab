# ADR 0012: Repositório-alvo separado, em Python

- **Status:** Aceita
- **Data:** 2026-11-24

## Contexto

O agente precisa de um repositório real, com testes e CI, para abrir PRs. Usar o próprio lab misturaria quem constrói com quem é avaliado.

## Decisão

Os PRs vão para o `agentic-delivery-target`: uma API pequena em Python (FastAPI), com `pytest`, `ruff` e GitHub Actions como checks obrigatórios, e branch protection com revisão exigida na `main`.

## Consequências

- O agente nunca mexe no lab, e o alvo mostra que ele não depende da stack do lab.
- O merge continua sendo de uma pessoa: a branch protection exige aprovação.
