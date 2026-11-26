# agentic-delivery-lab

Lab de desenvolvimento agêntico: um board de cards ligado a agentes de IA e a uma esteira de entrega. Cada card pronto vira um run de agente, que trabalha num ambiente isolado, abre um pull request e passa pelos mesmos gates de CI que qualquer código escrito por uma pessoa. O merge continua sendo humano.

> Projeto demonstrativo e em construção. Esta versão traz a base do repositório e o desenho da arquitetura; o código entra conforme o roadmap.

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

O card só anda quando um gate deixa: o run precisa entregar um handoff válido, o CI precisa ficar verde e uma pessoa precisa aprovar. CI vermelho e pedido de mudança na revisão devolvem o card ao agente. Qualquer falha terminal fica registrada com um código, para dar para contar e entender por que o agente parou.

Os diagramas seguem o tema claro ou escuro do leitor.

## Princípios

- **Regra que importa vira gate.** Agente ignora documento, mas não passa por um check vermelho.
- **Texto de card é dado, nunca instrução.** Issue e comentário podem tentar manipular o agente (prompt injection); o worker trata esse conteúdo como entrada a analisar.
- **Credencial de vida curta.** GitHub App com token curto e OIDC no CI, nunca chave estática.
- **Poucas tools, bem descritas.** Tool demais piora a escolha do agente.
- **Estado tipado.** Todo run termina em um estado conhecido, e falha sempre carrega um código.

## Como um card vira PR

O board ainda é a linha de comando (o board htmx é o M3), mas o fluxo já sai do banco:

1. **`adl card create`** grava o card no Postgres, no backlog. **`adl card ready`** move o card para Pronto e enfileira um run; um card nunca tem dois runs ativos.
2. **`adl orchestrator`** tira o run da fila e sobe um container de worker só para ele, sem bind mount, sem capabilities e com limites de memória e processos.
3. **O worker** recebe só a chave da Anthropic. Clona o repo-alvo (público, sem token), instala as dependências e roda o Claude Code headless com a skill do card. O agente só pode ler, editar e rodar `ruff`, `pytest`, `git status` e `git diff`: sem rede, sem push. O handoff sai em JSON validado por schema.
4. **O orquestrador** copia o `result.json` e o `changes.patch` do container, confere de novo os caminhos do patch e, só então, usa a chave do GitHub App: commit como o bot do App, push, PR e revogação do token.
5. O run, o custo, os turnos, o handoff, as tools negadas e o log do worker vão para o banco, e o card passa para `pr_open` ou para `failed` com um [código](docs/roadmap.md#códigos-de-falha).

O agente nunca coexiste com a credencial do GitHub.

## Executar localmente

Requisitos: Docker, o GitHub App configurado ([`docs/github-app.md`](docs/github-app.md)) e uma chave da Anthropic.

```sh
cp .env.example .env    # preencha App ID, Installation ID e ANTHROPIC_API_KEY
docker compose up -d --build
```

Isso sobe o Postgres e o orquestrador. Para mandar um card:

```sh
docker compose run --rm cli card create /lab/cards/delete-task.json
docker compose run --rm cli card ready ADL-2
docker compose run --rm cli card list
docker compose logs -f orchestrator
```

O orquestrador fala com o Docker pelo socket e roda como root só por isso; o worker que roda o agente continua sem privilégios. Para rodar um card sem banco nem orquestrador, há o `scripts/run-card.sh`.

Os testes rodam sem Go instalado. Os do banco precisam de um Postgres em `TEST_DATABASE_URL` e são pulados sem ele:

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go test ./...
```

## Estado atual

**M2 concluído** em 26 de novembro de 2026. O card `ADL-2` ([`cards/delete-task.json`](cards/delete-task.json), adicionar `DELETE /tasks/{id}`) saiu do banco e passou pelos dois caminhos:

- **Falha com código:** o primeiro run terminou como `workspace_error`, porque o worker sem root não conseguia criar `/out` na imagem. O card foi para `failed`, nada foi publicado, e o detalhe e o log do worker ficaram no banco.
- **Sucesso:** corrigida a imagem, o mesmo card voltou para a fila e virou o [PR #3](https://github.com/thallesdaniell/agentic-delivery-target/pull/3): 4 arquivos, `lint` e `test` verdes, US$ 0,05, 13 turnos e 27 segundos. O card foi para `pr_open` e a entrega foi registrada com o número do PR.

Os 6 testes do banco rodam contra um Postgres real: fila sem duplicidade, falha sem código recusada pelo banco, recuperação de runs interrompidos e o registro da entrega. Não foi provocado um reinício do orquestrador no meio de um run real.

Marcos anteriores: o M1 (o worker sozinho transformou o card `ADL-1` no [PR #2](https://github.com/thallesdaniell/agentic-delivery-target/pull/2)) e o M0 (repo-alvo com CI obrigatório e GitHub App).

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
- [ ] M3: board htmx
- [ ] M4: ciclo fechado com CI, revisão e merge
- [ ] M5: Kubernetes Job e webhook

## Licença

[MIT](LICENSE)
