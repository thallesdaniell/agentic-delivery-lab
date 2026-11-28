# ADR 0011: Fluxo puxado: o card só anda para frente e uma pessoa decide cada passo que gasta

- **Status:** Aceita
- **Data:** 2026-11-27

## Contexto

A primeira versão do M4 devolvia o card ao agente sozinha: CI vermelho ou pedido de mudanças moviam o card de volta para Pronto, até 2 vezes. Na prova de ponta a ponta apareceram dois problemas. O agente reverteu um commit feito por uma pessoa no PR, sem perguntar. E uma falha de infraestrutura gastou uma tentativa do agente, que não tinha como resolvê-la. Num board ágil o card também não anda para trás: o que acontece vira sinal no card.

## Decisão

- As colunas só vão para frente: Backlog → Pronto → Executando → Em revisão → Concluído. Falhou é fim de linha: a implementação falhou ou o PR foi fechado.
- **Pronto** é "refinado, esperando alguém puxar" e não gasta nada. **Entrar em Executando** enfileira o run.
- Arrastar e botões seguem a mesma regra, `store.AllowedMoves`, que o banco aplica em `Move`. O board marca em cada card as colunas que o aceitam; soltar fora delas é recusado pelo servidor.
- Em revisão, o observador só atualiza as etiquetas (CI rodando, CI vermelho, mudanças pedidas, aguardando revisão, aprovado) e deixa pronta a correção: o log do CI ou os comentários da revisão.
- Uma pessoa decide pelos botões: **Corrigir com o agente**, com orientação opcional que vai ao agente delimitada, ou **Rodar o CI de novo**, que refaz os jobs vermelhos sem gastar agente.
- A correção roda com o card parado em Em revisão, com a etiqueta "corrigindo". Se ela falhar, o card continua em revisão com o código na etiqueta. No limite (2 correções de CI, 3 rodadas de revisão), o botão some.

## Consequências

- Substitui o retorno automático da primeira versão do M4; os códigos `ci_failed_after_retries` e `review_limit_reached` saíram da lista.
- O agente nunca age sozinho num PR em que uma pessoa mexeu.
- Mais cliques que o modo automático; um "piloto automático" por card pode voltar como opção, registrado numa ADR nova.
