# ADR 0006: Board próprio em htmx, num processo sem segredo

- **Status:** Aceita
- **Data:** 2026-11-26

## Contexto

O card precisa mostrar agente, skill, run, handoff, custo, tools negadas e código de falha. O GitHub Projects não tem esses campos, e um board que dispara runs gasta dinheiro a cada clique.

## Decisão

Board próprio, renderizado no servidor com htmx 2.0.4 embutido no binário. Ele é um processo separado que só fala com o Postgres: sem socket do Docker e sem segredo. Escrita só com o header do htmx e da mesma origem, CSP restrita (`script-src 'self'`) e confirmação antes de qualquer ação que gaste dinheiro.

## Consequências

- Ações que precisam do GitHub (rodar o CI de novo) viram pedido no banco, que o orquestrador executa.
- O JavaScript próprio (o arrastar do board) é servido pelo board, sem script inline.
- O board acompanha o andamento por polling de 3 segundos.
