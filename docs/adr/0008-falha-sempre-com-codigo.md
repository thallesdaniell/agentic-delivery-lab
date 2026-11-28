# ADR 0008: Falha sempre com código, de uma lista fechada

- **Status:** Aceita
- **Data:** 2026-11-24

## Contexto

Sem código, uma falha vira texto livre e não dá para contar nem comparar por que os agentes param.

## Decisão

Todo run que termina como Falhou carrega um código de uma lista fechada em `internal/run/run.go`, e o card que falha no ciclo de revisão também (`pr_closed`). O banco tem a mesma lista numa constraint, e um teste confere que as duas batem. O `result.json` com falha e código fora da lista é recusado.

## Consequências

- A lista cresce por marco e está documentada no roadmap.
- Uma correção que falha deixa o código no run sem mover o card (ver a ADR 0011).
