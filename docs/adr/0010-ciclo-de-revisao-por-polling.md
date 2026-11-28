# ADR 0010: Ciclo de revisão acompanhado por polling do GitHub

- **Status:** Aceita
- **Data:** 2026-11-27

## Contexto

Depois do PR aberto, o card precisa refletir o CI, as revisões e o merge. O GitHub avisa por webhook, mas o lab roda local, sem endereço público.

## Decisão

Um observador dentro do orquestrador consulta o GitHub a cada 30 segundos (`REVIEW_POLL_INTERVAL`) para cada card em revisão sem run ativo: estado do PR, checks do último commit e revisões. A decisão é uma função pura (`internal/review/decide.go`). Merge conclui o card; PR fechado sem merge leva o card para Falhou com `pr_closed`. O webhook entra no M5.

## Consequências

- Sem túnel nem endpoint público no ambiente local.
- Até 30 segundos de atraso entre o GitHub e o board.
- O observador só baixa log e comentários quando a fase muda, para não repetir chamadas.
