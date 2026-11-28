# ADR 0002: Orquestrador e board em Go, num binário só

- **Status:** Aceita
- **Data:** 2026-11-24

## Contexto

O lab precisa de fila, estado dos runs, cliente do Docker, cliente do GitHub e um board web. Dá para fazer isso com vários serviços ou com um binário só.

## Decisão

Um binário `adl`, em Go, com subcomandos: `orchestrator`, `board`, `worker`, `publish`, `card`, `catalog` e `migrate`. Sem framework web e sem SDK do Docker ou do GitHub: a biblioteca padrão faz HTTP, o JWT do App e o cliente do Docker pelo socket.

## Consequências

- Uma imagem só serve para o orquestrador, o board e o worker, cada um com permissões diferentes no compose.
- Poucas dependências para auditar; o custo é escrever à mão os poucos endpoints usados.
- O mesmo padrão do `sre-agent`: Postgres, migrations embutidas e testes que rodam em Docker, sem Go instalado.
