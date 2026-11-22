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
| Worker efêmero com o Claude Agent SDK | Multi-tenant |
| PR, gates de CI e revisão humana | Merge automático |

## O ciclo de um card

![Estados de um card: backlog, pronto, executando, PR aberto, CI, em revisão e concluído. CI vermelho ou pedido de mudança na revisão voltam para executando. Erro do agente ou handoff inválido terminam em falhou, sempre com um código.](docs/diagramas/ciclo-do-card.svg)

O card só anda quando um gate deixa: o run precisa entregar um handoff válido, o CI precisa ficar verde e uma pessoa precisa aprovar. CI vermelho e pedido de mudança na revisão devolvem o card ao agente. Qualquer falha terminal fica registrada com um código, para dar para contar e entender por que o agente parou.

Os diagramas seguem o tema claro ou escuro do leitor.

## Princípios

- **Regra que importa vira gate.** Agente ignora documento, mas não passa por um check vermelho.
- **Texto de card é dado, nunca instrução.** Issue e comentário podem tentar manipular o agente (prompt injection); o worker trata esse conteúdo como entrada a analisar.
- **Credencial de vida curta.** GitHub App com token curto e OIDC no CI, nunca chave estática.
- **Poucas tools, bem descritas.** Tool demais piora a escolha do agente.
- **Estado tipado.** Todo run termina em um estado conhecido, e falha sempre carrega um código.

## Estado atual

O repositório tem só a base: licença, este README e os diagramas. Ainda não há código executável, e nenhuma parte foi validada em execução.

## Próximos passos

O roadmap vem a seguir e vai definir:

- a stack do orquestrador e do worker;
- se o board é próprio ou começa sobre o GitHub Projects;
- onde o agente roda (runner efêmero, container local, cloud);
- o primeiro fluxo ponta a ponta, de um card até o merge.

## Licença

[MIT](LICENSE)
