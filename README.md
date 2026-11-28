# agentic-delivery-lab

Lab de desenvolvimento agêntico: um board de cards ligado a agentes de IA e a uma esteira de entrega. Cada card pronto vira um run de agente, que trabalha num ambiente isolado, abre um pull request e passa pelos mesmos gates de CI que qualquer código escrito por uma pessoa. O merge continua sendo humano.

> Projeto demonstrativo e em construção. O fluxo de card a PR já roda pelo board; o ciclo de CI, revisão e merge entra conforme o roadmap.

![Um card pronto no board dispara o orquestrador, que cria um run e sobe um worker efêmero. O worker carrega uma skill, conversa com a API do Claude, abre um pull request e devolve um handoff ao orquestrador. O PR passa pelo CI; vermelho volta ao agente, verde vai para a revisão humana, e o merge conclui o card.](docs/diagramas/visao-geral.svg)

## O que vai demonstrar

- Board com cards (objetivo, critérios de aceite, repositório) e um agente atribuído a cada card.
- Skills versionadas (`SKILL.md`) e agentes definidos como a combinação de skill, modelo e tools.
- Um run por card, num container efêmero, com estado e logs registrados.
- Handoff do agente em JSON com schema validado: sem handoff válido, o run termina como falho, com um código.
- Pull request aberto pelo agente e gates de CI obrigatórios (lint, typecheck, testes).
- Merge sempre aprovado por uma pessoa.

## Escopo desta fase

| Dentro | Fora, por enquanto |
|---|---|
| Board agêntico: cards, skills e agentes | Métricas (lead time, custo por card, taxa de sucesso por skill) |
| Orquestrador com fila e estado dos runs | Controle de acesso (RBAC, SSO) |
| Worker efêmero com o Claude Code headless | Multi-tenant |
| PR, gates de CI e revisão humana | Merge automático |

## O ciclo de um card

![Estados de um card: backlog, pronto, executando, PR aberto, CI, em revisão e concluído. CI vermelho (até 2 vezes) ou pedido de mudança na revisão voltam para executando. Erro do agente ou handoff inválido terminam em falhou, sempre com um código.](docs/diagramas/ciclo-do-card.svg)

O card só anda quando um gate deixa: o run precisa entregar um handoff válido, o CI precisa ficar verde e uma pessoa precisa aprovar. No board o card nunca anda para trás: CI vermelho e pedido de mudança viram etiquetas no card, e uma pessoa decide se manda a correção ao agente ([ADR 0011](docs/adr/0011-fluxo-puxado-o-card-so-anda-para-frente.md)). Qualquer falha terminal fica registrada com um código, para dar para contar e entender por que o agente parou.

Os diagramas seguem o tema claro ou escuro do leitor.

## Princípios

- **Regra que importa vira gate.** Agente ignora documento, mas não passa por um check vermelho.
- **Texto de card é dado, nunca instrução.** Issue e comentário podem tentar manipular o agente (prompt injection); o worker trata esse conteúdo como entrada a analisar.
- **Credencial de vida curta.** GitHub App com token curto e OIDC no CI, nunca chave estática.
- **Poucas tools, bem descritas.** Tool demais piora a escolha do agente.
- **Estado tipado.** Todo run termina em um estado conhecido, e falha sempre carrega um código.

## Como um card vira PR

1. **No board** (`http://localhost:8090`), crie o card: objetivo, critérios de aceite e repositório. Ele nasce no backlog, com ou sem agente.
2. **Atribua um agente** do catálogo. Um agente é uma skill (`skills/<nome>/SKILL.md`), um modelo, um teto de custo e um subconjunto das tools que o worker permite, definido em `agents/<nome>.json`.
3. **Arraste o card.** Pronto marca o card como refinado e não gasta nada. **Soltar em Executando** (ou o botão Executar) grava um run na fila com o agente e a skill daquele momento, com a versão de cada um (o hash do conteúdo). O board só deixa soltar onde a regra permite, a mesma que o servidor aplica.
4. **O orquestrador** tira o run da fila e sobe um container de worker só para ele, sem bind mount, sem capabilities e com limites de memória e processos.
5. **O worker** recebe só a chave da Anthropic. Clona o repo-alvo, instala as dependências e roda o Claude Code headless com a skill e as tools do agente: sem rede e sem push. O handoff sai em JSON validado por schema, e o log do worker aparece no board enquanto o run roda.
6. **O orquestrador** confere de novo os caminhos do patch e, só então, usa a chave do GitHub App: commit como o bot do App, push, PR e revogação do token. O card vai para **Em revisão**.
7. **Em revisão**, o orquestrador acompanha o PR e atualiza a etiqueta do card. Com CI vermelho ou pedido de mudanças, o card mostra **Corrigir com o agente** (com orientação opcional) e, no CI vermelho, **Rodar o CI de novo**. A correção roda no mesmo PR, sem tirar o card da coluna.
8. **O merge**, feito por uma pessoa, leva o card para **Concluído**.

O agente nunca coexiste com a credencial do GitHub, e o board também não: ele é outro processo, que só fala com o Postgres. Ele não tem o socket do Docker nem segredo, e aceita escrita só do próprio board (header do htmx e mesma origem), porque executar um card gasta dinheiro. As decisões estão registradas em [`docs/adr/`](docs/adr/README.md).

## Executar localmente

Requisitos: Docker, o GitHub App configurado ([`docs/github-app.md`](docs/github-app.md)) e uma chave da Anthropic.

```sh
cp .env.example .env    # preencha App ID, Installation ID e ANTHROPIC_API_KEY
docker compose up -d --build
```

Isso sobe o Postgres, o orquestrador e o board em `http://localhost:8090` (a variável `BOARD_PORT` muda a porta). Ao subir, o orquestrador e o board aplicam as migrations e copiam `agents/` e `skills/` para o banco.

O mesmo fluxo existe na linha de comando:

```sh
docker compose run --rm cli card create /lab/cards/delete-task.json
docker compose run --rm cli card assign ADL-2 python-dev
docker compose run --rm cli card start ADL-2           # o mesmo que soltar em Executando
docker compose run --rm cli card fix ADL-2 "orientação"  # manda a correção pendente do PR ao agente
docker compose run --rm cli card list
docker compose logs -f orchestrator
```

O orquestrador fala com o Docker pelo socket e roda como root só por isso; o worker que roda o agente e o board continuam sem privilégios. Para rodar um card sem banco nem orquestrador, há o `scripts/run-card.sh`.

Os testes rodam sem Go instalado. Os do banco precisam de um Postgres em `TEST_DATABASE_URL` e são pulados sem ele (eles apagam as tabelas: use um banco descartável):

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go test ./...
```

## Estado atual

**M4 em andamento.** O que já foi provado com PRs reais:

- **Merge conclui o card.** Ao subir o observador, o `ADL-2` (PR #3) e o `ADL-3` (PR #4), já mergeados, foram para Concluído sozinhos. O `ADL-4` foi para Concluído logo depois do merge do [PR #5](https://github.com/thallesdaniell/agentic-delivery-target/pull/5).
- **CI vermelho volta ao agente com o log.** Um commit de teste quebrou o `GET /health` no PR #5. O observador baixou o log do job, e a correção (run 6: US$ 0,01, 6 turnos) achou a causa, restaurou o código sem mexer nos testes, empurrou no mesmo PR e comentou o que fez. O CI ficou verde. A primeira tentativa (run 5) falhou na publicação por um bug do lab, já corrigido.
- **Arrastar segue a regra.** O `ADL-5` foi arrastado do Backlog para Pronto sem criar run, e soltar o card em Concluído foi recusado.

A prova acima rodou com o retorno automático da primeira versão do M4; o fluxo puxado da [ADR 0011](docs/adr/0011-fluxo-puxado-o-card-so-anda-para-frente.md) já está no ar e coberto por testes. Falta provar ao vivo, com ele, o retorno pela revisão: um "Request changes" de verdade e o botão **Corrigir com o agente**.

Marcos anteriores:

- **M3:** o card `ADL-3` fez o fluxo inteiro pela UI e virou o [PR #4](https://github.com/thallesdaniell/agentic-delivery-target/pull/4) (US$ 0,04, 10 turnos, 22 s), com o log do worker ao vivo no board.
- **M2:** o card `ADL-2` saiu do banco pela linha de comando. Passou por uma falha com código (`workspace_error`) e, depois de corrigida a imagem, virou o [PR #3](https://github.com/thallesdaniell/agentic-delivery-target/pull/3).
- **M1:** o worker sozinho transformou o card `ADL-1` no [PR #2](https://github.com/thallesdaniell/agentic-delivery-target/pull/2), que já passou pelo merge.
- **M0:** repo-alvo com CI obrigatório e GitHub App.

## Roadmap

O plano completo, com as decisões e os critérios de pronto de cada marco, está em [`docs/roadmap.md`](docs/roadmap.md). Em resumo:

- **Stack:** orquestrador e board em Go; o agente é o Claude Code headless, num container por run.
- **Board próprio**, com o Postgres como fonte da verdade; o GitHub fica com branch, PR e CI.
- **Execução:** Docker local primeiro, Kubernetes Job depois.
- **Repo-alvo** separado, em Python, onde o agente abre os PRs.
- **Limites:** até 2 tentativas de correção com CI vermelho e um teto de custo por run.

- [x] M0: repo-alvo em Python com CI obrigatório e GitHub App
- [x] M1: o worker sozinho transforma um card em PR
- [x] M2: orquestrador com Postgres, fila e runs tipados
- [x] M3: board htmx e catálogo de agentes e skills
- [ ] M4: ciclo fechado com CI, revisão e merge (em andamento)
- [ ] M5: Kubernetes Job e webhook

## Licença

[MIT](LICENSE). O board embute o [htmx](https://htmx.org) 2.0.4, distribuído sob a licença 0BSD.
