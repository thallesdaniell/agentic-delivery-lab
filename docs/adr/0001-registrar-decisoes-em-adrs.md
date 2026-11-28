# ADR 0001: Registrar decisões em ADRs

- **Status:** Aceita
- **Data:** 2026-11-27

## Contexto

O lab cresceu por marcos (M0 a M4) e as decisões ficaram espalhadas entre a tabela do roadmap, mensagens de commit e conversas. Algumas já foram trocadas (o retorno automático do M4 virou fluxo puxado), e sem registro não dá para saber o que vale e por quê.

## Decisão

Cada decisão de arquitetura ou de fluxo vira um arquivo em `docs/adr/`, numerado, com status, contexto, decisão e consequências. Decisão trocada não é apagada: a antiga fica com status **Substituída por** e aponta para a nova. O roadmap continua com o plano e os critérios de pronto, e linka as ADRs.

## Consequências

- Quem chega ao repositório entende o porquê de cada escolha sem ler o histórico inteiro.
- Mudar uma decisão custa um arquivo novo, o que força escrever o motivo.
- As ADRs 0002 a 0010 registram, com a data em que foram tomadas, decisões anteriores a esta.
