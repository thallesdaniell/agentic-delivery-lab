# ADR 0004: GitHub App com token curto, e o agente nunca junto da credencial

- **Status:** Aceita
- **Data:** 2026-11-24

## Contexto

Quem abre o PR precisa de credencial de escrita no repositório. Se o agente tiver essa credencial, um card com prompt injection pode fazer o agente usá-la para outra coisa.

## Decisão

O lab age como um GitHub App instalado só no repositório-alvo, com token de instalação de 1 hora, revogado depois do uso. A execução tem duas etapas: o container do agente recebe só a chave da Anthropic e devolve um patch; depois, o orquestrador confere de novo os caminhos do patch (nada em `.github/`) e só então usa a chave do App para commit, push, PR e comentário. O board não tem segredo nenhum.

## Consequências

- O agente nunca coexiste com a credencial do GitHub.
- O App não tem permissão de Workflows: o GitHub recusa push que altere o CI, então o agente não muda o que o avalia.
- O observador do M4 usa o mesmo App (leitura de PR, checks e logs, e Actions: write para refazer jobs), com o token em cache e renovado antes de expirar.
