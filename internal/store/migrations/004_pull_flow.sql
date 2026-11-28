-- Fluxo puxado (M4): o card só anda para frente e uma pessoa decide cada passo que gasta dinheiro.
-- Pronto passa a ser "refinado, esperando alguém puxar"; entrar em Executando é que enfileira o run.
-- O run de correção não move o card: ele fica em Em revisão, e a etiqueta mostra a correção.

-- A correção pendente é a que o botão "Corrigir com o agente" dispara: o observador a deixa pronta
-- (log do CI ou comentários da revisão) e a pessoa decide se manda para o agente.
ALTER TABLE card ADD COLUMN pending_review_id bigint;

-- "Rodar o CI de novo": o board só registra o pedido; o observador, que tem o token do App, o executa.
ALTER TABLE card ADD COLUMN ci_rerun_requested boolean NOT NULL DEFAULT false;

-- Orientação opcional de quem pediu a correção no board.
ALTER TABLE run ADD COLUMN note text;

-- Com o fluxo puxado, os limites desligam o botão em vez de encerrar o card:
-- só o PR fechado sem merge leva o card para Falhou com código de card.
ALTER TABLE card DROP CONSTRAINT card_failure_code_check;
ALTER TABLE card ADD CONSTRAINT card_failure_code_check CHECK (failure_code IN ('pr_closed'));

ALTER TABLE run DROP CONSTRAINT run_failure_code_check;
ALTER TABLE run ADD CONSTRAINT run_failure_code_check CHECK (failure_code IN (
    'card_invalid', 'workspace_error', 'handoff_invalid', 'agent_blocked', 'agent_error',
    'model_api_error', 'budget_exceeded', 'timeout', 'no_changes', 'forbidden_path',
    'publish_error', 'interrupted', 'pr_closed'));
