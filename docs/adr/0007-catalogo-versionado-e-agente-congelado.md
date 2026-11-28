# ADR 0007: Catálogo versionado por hash e agente congelado no run

- **Status:** Aceita
- **Data:** 2026-11-26

## Contexto

Um agente é uma skill, um modelo, um teto de custo e um conjunto de tools. Se o catálogo mudar com um run na fila, o run não pode rodar uma mistura do antes e do depois.

## Decisão

O catálogo vive no repositório (`skills/<nome>/SKILL.md` e `agents/<nome>.json`) e é copiado para o banco ao subir. A versão é o hash do conteúdo, e a do agente inclui a da skill. Enfileirar um run congela o agente inteiro em `run.spec`, e o worker roda exatamente isso. Um agente só restringe as tools do worker, nunca amplia.

## Consequências

- Cada run registra a versão do agente e da skill que usou.
- O run de correção usa o agente congelado do run anterior: o mesmo PR não troca de agente no meio.
- Agente que sai do repositório fica inativo, e os runs antigos continuam apontando para ele.
