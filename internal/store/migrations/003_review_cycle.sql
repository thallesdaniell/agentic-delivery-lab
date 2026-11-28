-- Ciclo de revisão (M4): o card acompanha o PR até o merge.
-- "PR aberto" vira "Em revisão", com a fase do PR numa coluna à parte, e entra "Concluído".

ALTER TABLE card DROP CONSTRAINT card_status_check;
UPDATE card SET status = 'in_review' WHERE status = 'pr_open';
ALTER TABLE card ADD CONSTRAINT card_status_check
    CHECK (status IN ('backlog', 'ready', 'running', 'in_review', 'done', 'failed'));

ALTER TABLE card
    -- Fase do PR enquanto o card está em revisão.
    ADD COLUMN review_phase   text
        CHECK (review_phase IN ('ci_pending', 'ci_failed', 'awaiting_review', 'changes_requested', 'approved')),
    ADD COLUMN ci_attempts    integer NOT NULL DEFAULT 0,
    ADD COLUMN review_rounds  integer NOT NULL DEFAULT 0,
    -- Última revisão com pedido de mudanças já devolvida ao agente: a mesma revisão não dispara duas vezes.
    ADD COLUMN last_review_id bigint,
    -- A correção que o card recebeu por último; "Tentar de novo" repete esta, e não reimplementa o card.
    ADD COLUMN rework_kind     text CHECK (rework_kind IN ('fix_ci', 'address_review')),
    ADD COLUMN rework_feedback text,
    -- Falha do card sem falha de run: o CI não ficou verde, a revisão não convergiu ou o PR foi fechado.
    ADD COLUMN failure_code   text CHECK (failure_code IN ('ci_failed_after_retries', 'review_limit_reached', 'pr_closed')),
    ADD COLUMN failure_detail text,
    ADD COLUMN done_at        timestamptz;
ALTER TABLE card ADD CONSTRAINT card_phase_only_in_review CHECK (review_phase IS NULL OR status = 'in_review');
ALTER TABLE card ADD CONSTRAINT card_code_only_when_failed CHECK (failure_code IS NULL OR status = 'failed');

-- O run passa a ter um tipo; um run de correção leva o log do CI ou os comentários da revisão.
ALTER TABLE run
    ADD COLUMN kind     text NOT NULL DEFAULT 'implement' CHECK (kind IN ('implement', 'fix_ci', 'address_review')),
    ADD COLUMN feedback text,
    ADD COLUMN attempt  integer;
ALTER TABLE run ADD CONSTRAINT run_rework_has_feedback CHECK (kind = 'implement' OR feedback IS NOT NULL);

-- A lista de códigos do run acompanha internal/run/run.go (um teste confere).
ALTER TABLE run DROP CONSTRAINT run_failure_code_check;
ALTER TABLE run ADD CONSTRAINT run_failure_code_check CHECK (failure_code IN (
    'card_invalid', 'workspace_error', 'handoff_invalid', 'agent_blocked', 'agent_error',
    'model_api_error', 'budget_exceeded', 'timeout', 'no_changes', 'forbidden_path',
    'publish_error', 'interrupted', 'ci_failed_after_retries', 'review_limit_reached', 'pr_closed'));
