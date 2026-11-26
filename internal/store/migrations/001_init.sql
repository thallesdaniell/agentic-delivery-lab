-- Cards, runs e entregas do orquestrador (M2).
-- Os códigos de falha da constraint precisam bater com internal/run/run.go: um teste confere.

CREATE TABLE card (
    id                  text PRIMARY KEY,
    title               text NOT NULL,
    objective           text NOT NULL,
    acceptance_criteria jsonb NOT NULL,
    repo                text NOT NULL,
    skill               text NOT NULL,
    status              text NOT NULL DEFAULT 'backlog'
        CHECK (status IN ('backlog', 'ready', 'running', 'pr_open', 'failed')),
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE run (
    id                 bigserial PRIMARY KEY,
    card_id            text NOT NULL REFERENCES card (id),
    status             text NOT NULL DEFAULT 'queued'
        CHECK (status IN ('queued', 'running', 'succeeded', 'failed')),
    failure_code       text
        CHECK (failure_code IN (
            'card_invalid', 'workspace_error', 'handoff_invalid', 'agent_blocked', 'agent_error',
            'model_api_error', 'budget_exceeded', 'timeout', 'no_changes', 'forbidden_path',
            'publish_error', 'interrupted')),
    detail             text,
    model              text,
    cost_usd           numeric(10, 4),
    num_turns          integer,
    duration_sec       numeric(10, 1),
    handoff            jsonb,
    changed_files      jsonb,
    permission_denials jsonb,
    log                text,
    container_id       text,
    created_at         timestamptz NOT NULL DEFAULT now(),
    started_at         timestamptz,
    finished_at        timestamptz,
    -- Falha sempre com código; sucesso nunca com código.
    CONSTRAINT run_failure_has_code CHECK ((status = 'failed') = (failure_code IS NOT NULL))
);

-- Um card tem no máximo um run ativo: a fila não duplica trabalho.
CREATE UNIQUE INDEX run_one_active_per_card ON run (card_id) WHERE status IN ('queued', 'running');
CREATE INDEX run_queued ON run (id) WHERE status = 'queued';

CREATE TABLE delivery (
    id         bigserial PRIMARY KEY,
    run_id     bigint NOT NULL UNIQUE REFERENCES run (id),
    card_id    text NOT NULL REFERENCES card (id),
    repo       text NOT NULL,
    branch     text NOT NULL,
    pr_number  integer NOT NULL,
    pr_url     text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
