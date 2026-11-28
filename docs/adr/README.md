# Decisões de arquitetura (ADRs)

Cada arquivo registra uma decisão: o contexto, o que foi decidido e as consequências. Decisão trocada não é apagada; ela aponta para a que a substituiu ([ADR 0001](0001-registrar-decisoes-em-adrs.md)).

| ADR | Decisão | Status | Data |
|---|---|---|---|
| [0001](0001-registrar-decisoes-em-adrs.md) | Registrar decisões em ADRs | Aceita | 2026-11-27 |
| [0002](0002-orquestrador-e-board-em-go.md) | Orquestrador e board em Go, num binário só | Aceita | 2026-11-24 |
| [0003](0003-agente-claude-code-headless.md) | O agente é o Claude Code headless, um container por run | Aceita | 2026-11-24 |
| [0004](0004-credencial-github-app-em-duas-etapas.md) | GitHub App com token curto, e o agente nunca junto da credencial | Aceita | 2026-11-24 |
| [0005](0005-postgres-fonte-da-verdade-e-fila.md) | Postgres como fonte da verdade e como fila | Aceita | 2026-11-24 |
| [0006](0006-board-proprio-htmx-sem-segredo.md) | Board próprio em htmx, num processo sem segredo | Aceita | 2026-11-26 |
| [0007](0007-catalogo-versionado-e-agente-congelado.md) | Catálogo versionado por hash e agente congelado no run | Aceita | 2026-11-26 |
| [0008](0008-falha-sempre-com-codigo.md) | Falha sempre com código, de uma lista fechada | Aceita | 2026-11-24 |
| [0009](0009-teto-de-custo-por-run.md) | Teto de custo por run desde o primeiro marco | Aceita | 2026-11-24 |
| [0010](0010-ciclo-de-revisao-por-polling.md) | Ciclo de revisão acompanhado por polling do GitHub | Aceita | 2026-11-27 |
| [0011](0011-fluxo-puxado-o-card-so-anda-para-frente.md) | Fluxo puxado: o card só anda para frente e uma pessoa decide cada passo que gasta | Aceita | 2026-11-27 |
| [0012](0012-repo-alvo-separado-em-python.md) | Repositório-alvo separado, em Python | Aceita | 2026-11-24 |
