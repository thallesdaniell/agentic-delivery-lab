-- Catálogo de skills e agentes (M3). Os arquivos do repositório são a origem; estas tabelas
-- são a cópia sincronizada que o board lista e que o run usa para congelar o agente.

CREATE TABLE skill (
    name        text PRIMARY KEY,
    description text NOT NULL,
    content     text NOT NULL,
    version     text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE agent (
    name        text PRIMARY KEY,
    description text NOT NULL,
    skill       text NOT NULL REFERENCES skill (name),
    model       text NOT NULL,
    budget_usd  numeric(10, 2) NOT NULL CHECK (budget_usd > 0),
    tools       jsonb NOT NULL,
    version     text NOT NULL,
    -- Agente que saiu do repositório fica inativo: os runs antigos continuam apontando para ele.
    active      boolean NOT NULL DEFAULT true,
    updated_at  timestamptz NOT NULL DEFAULT now()
);

-- A skill passa a vir do agente; o card guarda só o agente atribuído, que pode faltar no backlog.
ALTER TABLE card ADD COLUMN agent text REFERENCES agent (name);
ALTER TABLE card DROP COLUMN skill;

-- O run guarda o agente e a skill do momento em que entrou na fila.
ALTER TABLE run
    ADD COLUMN agent         text REFERENCES agent (name),
    ADD COLUMN agent_version text,
    ADD COLUMN skill_version text,
    ADD COLUMN spec          jsonb;
ALTER TABLE run ADD CONSTRAINT run_queued_has_spec CHECK (status <> 'queued' OR spec IS NOT NULL);
