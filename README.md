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

## Como um card vira PR (M1)

Um run usa dois containers da mesma imagem, um depois do outro:

1. **`adl worker`** recebe só a chave da Anthropic. Clona o repo-alvo (público, sem token), instala as dependências e roda o Claude Code headless com a skill do card. O agente só pode ler, editar e rodar `ruff`, `pytest`, `git status` e `git diff`: sem rede, sem push. O handoff sai em JSON validado por schema, e o worker grava `result.json` e `changes.patch`.
2. **`adl publish`** recebe só a chave do GitHub App, montada só leitura. Confere de novo os caminhos do patch, gera um token de instalação, faz o commit como o bot do App, o push e abre o PR, e revoga o token no fim.

O agente nunca coexiste com a credencial do GitHub. Qualquer falha encerra o run com um [código](docs/roadmap.md#códigos-de-falha), e nada é publicado.

## Executar localmente

Requisitos: Docker, o GitHub App configurado ([`docs/github-app.md`](docs/github-app.md)) e uma chave da Anthropic.

```sh
cp .env.example .env   # preencha App ID, Installation ID e ANTHROPIC_API_KEY
scripts/run-card.sh cards/health.json
```

O script builda a imagem, roda o worker e, se ele terminar com sucesso, o publisher. A saída é o `result.json` do run, com custo, turnos, arquivos alterados, handoff e o link do PR.

Os testes rodam sem Go instalado:

```sh
docker run --rm -v "$PWD":/src -w /src golang:1.24-bookworm go test ./...
```

## Estado atual

**M1 concluído** em 26 de novembro de 2026. O card [`cards/health.json`](cards/health.json) (`ADL-1`, adicionar `GET /health`) virou o [PR #2](https://github.com/thallesdaniell/agentic-delivery-target/pull/2) no repo-alvo:

- Autor do PR e dos commits: `agentic-delivery-lab-worker[bot]`.
- Diff de 3 arquivos (rota, teste e README), com `lint` e `test` verdes no CI.
- O PR ficou bloqueado só pela aprovação humana, que o repo-alvo exige desde o M1.
- O run custou US$ 0,04, com 12 turnos e 23 segundos (Claude Sonnet 5.5, teto de US$ 1,00).

O mapeamento das saídas do Claude Code para os códigos de falha (`budget_exceeded`, `model_api_error`, `handoff_invalid`, `agent_blocked`) foi conferido com saídas reais e está coberto por testes. Não foi provocado um handoff inválido num run completo.

O M0 (repo-alvo com CI obrigatório e GitHub App) foi concluído em 24 de novembro de 2026.

## Roadmap

O plano completo, com as decisões e os critérios de pronto de cada marco, está em [`docs/roadmap.md`](docs/roadmap.md). Em resumo:

- **Stack:** orquestrador e board em Go; o agente é o Claude Code headless, num container por run.
- **Board próprio**, com o Postgres como fonte da verdade; o GitHub fica com branch, PR e CI.
- **Execução:** Docker local primeiro, Kubernetes Job depois.
- **Repo-alvo** separado, em Python, onde o agente abre os PRs.
- **Limites:** até 2 tentativas de correção com CI vermelho e um teto de custo por run.

- [x] M0: repo-alvo em Python com CI obrigatório e GitHub App
- [x] M1: o worker sozinho transforma um card em PR
- [ ] M2: orquestrador com Postgres, fila e runs tipados
- [ ] M3: board htmx
- [ ] M4: ciclo fechado com CI, revisão e merge
- [ ] M5: Kubernetes Job e webhook

## Licença

[MIT](LICENSE)
