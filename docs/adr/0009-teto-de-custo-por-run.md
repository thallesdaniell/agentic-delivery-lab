# ADR 0009: Teto de custo por run desde o primeiro marco

- **Status:** Aceita
- **Data:** 2026-11-24

## Contexto

Um agente que não converge pode girar até gastar muito. O lab roda com a chave da Anthropic de uma pessoa.

## Decisão

Cada run tem teto em dólares: o menor entre o do agente e o global (`RUN_BUDGET_USD`), passado ao Claude Code por `--max-budget-usd`. Passar do teto termina o run com `budget_exceeded`, com o custo registrado. O ciclo de revisão tem limites próprios: 2 correções de CI e 3 rodadas de revisão por card.

## Consequências

- O board mostra o custo de cada run contra o teto.
- Nada que gaste dinheiro acontece sem uma pessoa pedir (ver a ADR 0011).
