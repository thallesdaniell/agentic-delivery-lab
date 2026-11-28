# ADR 0005: Postgres como fonte da verdade e como fila

- **Status:** Aceita
- **Data:** 2026-11-24

## Contexto

O estado do card e do run poderia morar no GitHub Projects, num banco ou em ambos. A fila poderia ser Redis ou Temporal.

## Decisão

O Postgres guarda cards, runs, entregas e catálogo, e é a fila: `SELECT … FOR UPDATE SKIP LOCKED` tira o run mais antigo, e um índice único parcial garante no máximo um run ativo por card. O GitHub fica só com branch, PR e CI. Migrations versionadas e embutidas no binário.

## Consequências

- Um lugar só para o estado; o board lê dali e não precisa do GitHub.
- Dois orquestradores nunca pegam o mesmo run.
- Sem Redis nem Temporal enquanto a carga não pedir.
