# Roadmap

Do zero até um card que vira PR, passa pelo CI e é concluído com o merge, primeiro com Docker local e depois num cluster Kubernetes. Métricas e controle de acesso ficam fora desta fase (ver o [README](../README.md#escopo-desta-fase)).

## Decisões

| Tema | Decisão | Por quê |
|---|---|---|
| Orquestrador | **Go**: API, fila, estado dos runs e board num binário só, com subcomandos | O mesmo padrão do `sre-agent`: Postgres, migrations, `Store` com versão em memória para testes, OpenTelemetry |
| Worker | **Claude Code headless** num container, um por run | O agente precisa editar arquivos, rodar testes e usar git; o Claude Code já traz essas tools |
| Board | **Próprio**, HTML renderizado pelo servidor com htmx | O card precisa de agente, skill, run, handoff e `failure_code`, que o GitHub Projects não tem |
| Fonte da verdade | **Postgres**; o GitHub fica só com branch, PR e CI | Um lugar só para o estado do card e do run |
| Fila | **Postgres** (`SELECT … FOR UPDATE SKIP LOCKED`) | Sem Redis nem Temporal enquanto não houver necessidade |
| Execução | **Docker local** primeiro, **Kubernetes Job** (`kind`) depois | Tudo sobe com `docker compose up`; o cluster entra quando o fluxo estiver provado |
| Credencial | **GitHub App** instalado só no repo-alvo, token de instalação por run (expira em 1 h) | Nada de chave estática; o container do agente leva só a chave do Claude, e a do App fica num container separado que roda depois |
| Repo-alvo | **Repositório separado em Python**, pequeno e com testes | O agente nunca mexe no próprio lab, e o alvo mostra que ele não depende da stack do lab |
| CI vermelho | **Até 2 tentativas de correção**; na seguinte, o run termina como `Falhou` | Evita loop caro com um agente que não converge |
| Custo | **Limite em dólares por run** desde o M1 | Um run fora de controle não pode virar uma conta surpresa |

## Códigos de falha

Todo run que termina como `Falhou` carrega um destes códigos. A lista vive em [`internal/run/run.go`](../internal/run/run.go), cresce conforme os marcos e nunca aceita falha sem código: o `result.json` com status `failed` e código fora da lista é recusado.

| Código | Quando |
|---|---|
| `card_invalid` | O card não passa na validação ou a skill dele não existe |
| `workspace_error` | Não foi possível clonar o repo ou instalar as dependências |
| `handoff_invalid` | O agente terminou sem handoff ou com um handoff que não passa no schema |
| `agent_blocked` | O agente declarou que não conseguiu cumprir o card (`outcome: blocked`) |
| `agent_error` | O Claude Code saiu com erro ou não produziu saída |
| `model_api_error` | A API do modelo recusou ou falhou (sem crédito, chave inválida, limite de taxa) |
| `budget_exceeded` | O run passou do teto de custo |
| `timeout` | O run passou do tempo máximo |
| `no_changes` | O agente declarou `done` sem alterar nenhum arquivo |
| `forbidden_path` | O agente alterou um caminho proibido, como `.github/` |
| `publish_error` | O push ou a abertura do PR falharam |
| `interrupted` | O orquestrador reiniciou com o run em andamento |
| `ci_failed_after_retries` | O CI continuou vermelho depois das 2 tentativas de correção (M4) |

## Marcos

### M0: repo-alvo e GitHub App

- Repositório separado em Python: uma API pequena com testes (`pytest`) e lint (`ruff`).
- GitHub Actions no repo-alvo com lint e testes como checks obrigatórios, mais branch protection na `main`.
- GitHub App com permissão só de conteúdo e pull request, instalado só no repo-alvo.

**Pronto quando:** um PR com teste quebrado fica bloqueado pelo CI.

### M1: o worker sozinho (concluído)

- Imagem do worker: Claude Code, git e o runtime do repo-alvo.
- Entrada: um card em JSON (objetivo, critérios de aceite, repositório) e a skill.
- O worker clona o repo, cria a branch do card, deixa o agente trabalhar, faz push e abre o PR.
- Saída: o handoff em JSON (número do PR, branch, resumo, limitações), validado por schema.
- Limite de custo e de tempo por run, com o custo do run registrado na saída.

**Pronto quando:** um card escrito à mão vira um PR real no repo-alvo, e um handoff inválido faz o worker terminar com `handoff_invalid`.

O M1 vem antes do orquestrador de propósito: ele responde se o agente entrega um PR aceitável. Se não entregar, o resto não importa.

### M2: orquestrador (concluído)

- Postgres com `card`, `run` e `delivery`, e migrations versionadas e embutidas no binário. Agente e Skill ganharam tabela no M3.
- Fila no Postgres: `FOR UPDATE SKIP LOCKED` para tirar da fila e um índice único que garante no máximo um run ativo por card.
- O orquestrador sobe um container de worker por run pela API do Docker, sem bind mount: o card vai por variável de ambiente e a saída é copiada de `/out` quando o container para. O container roda sem capabilities, com `no-new-privileges` e limites de memória e processos.
- A publicação roda dentro do orquestrador, que guarda a chave do App; o container do agente continua sem credencial do GitHub.
- O banco recusa run `failed` sem `failure_code`, e um teste confere que a constraint e a lista de códigos em Go são as mesmas. Ao reiniciar, o orquestrador encerra os runs que estavam em andamento com `interrupted`.

**Pronto quando:** um card criado pela linha de comando termina em `PR aberto` ou em `Falhou` com código, sem intervenção manual.

### M3: board (concluído)

- Board htmx renderizado pelo servidor, com colunas por estado e atualização a cada 3 segundos. Nele se cria o card, se atribui o agente, se move o card para Pronto e se veem os runs, o handoff, as tools negadas e o log do worker.
- O board é um processo separado do orquestrador e só fala com o Postgres: sem socket do Docker e sem segredo. A escrita exige o header do htmx e a mesma origem, e as páginas saem com CSP restrita. O htmx vai embutido no binário.
- Catálogo no repositório: `skills/<nome>/SKILL.md` e `agents/<nome>.json` (skill, modelo, teto de custo e tools). Um agente só restringe as tools do worker, nunca amplia. O orquestrador e o board copiam o catálogo para as tabelas `skill` e `agent` ao subir; agente que sai do repositório fica inativo.
- A versão de agente e skill é o hash do conteúdo. Mover o card para Pronto congela o agente no run (`run.spec`), e o worker roda exatamente isso. O banco recusa run na fila sem agente congelado.
- O card só vai para Pronto a partir do backlog ou de Falhou, e só com agente ativo. O teto de custo do run é o menor entre o do agente e o global (`RUN_BUDGET_USD`).
- O orquestrador grava o fim do log do worker no banco a cada 3 segundos, e o board mostra o andamento sem acessar o Docker.

**Pronto quando:** o fluxo do M2 sai inteiro da UI.

### M4: ciclo fechado

- Polling da API do GitHub para o estado do CI e do PR.
- CI vermelho devolve o card ao agente com o log da falha, até 2 vezes; depois, `ci_failed_after_retries`.
- Revisão com pedido de mudanças devolve o card ao agente com os comentários do PR.
- Merge feito por uma pessoa no GitHub conclui o card.

**Pronto quando:** o [ciclo do card](diagramas/ciclo-do-card.svg) funciona de ponta a ponta, inclusive os dois retornos.

### M5: cluster

- O worker vira um Kubernetes Job num `kind`, um por run, com o nome derivado do id do run.
- Webhook do GitHub no lugar do polling (via `smee.io` ou túnel no ambiente local).
- Rede de saída do worker restrita ao GitHub e à API do modelo.

**Pronto quando:** o mesmo fluxo do M4 roda no cluster.

## Depois desta fase

Métricas (lead time, custo por card, taxa de sucesso por skill), agente revisor, vários repositórios-alvo e controle de acesso.
