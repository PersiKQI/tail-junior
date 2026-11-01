-- Этап 2 спецификации v0.1 (§6, §9): диагностические и отложенные наборы, сроки по якорям,
-- финализация completed/partial/skipped, отзыв редакций и замена назначений.

-- +goose Up
CREATE TABLE assessment_sets (
    id                  text PRIMARY KEY,
    user_id             text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind                text NOT NULL CHECK (kind IN ('diagnostic', 'review')),
    slot_family_id      text NOT NULL,
    program_lineage     text NOT NULL,
    program_version     text NOT NULL REFERENCES program_releases (version),
    delay_days          integer,
    -- status — текущее состояние; available для review вычисляется по due_at при чтении.
    status              text NOT NULL CHECK (status IN ('waiting', 'available', 'in_progress', 'completed', 'partial', 'skipped', 'invalidated')),
    due_at              timestamptz,
    baseline_eligible   boolean,
    created_at          timestamptz NOT NULL,
    -- Первая финализация неизменна и сохраняется даже после invalidated (§6, §9).
    finalized_at        timestamptz,
    finalized_status    text CHECK (finalized_status IN ('completed', 'partial', 'skipped')),
    invalidated_at      timestamptz,
    invalidation_reason text,
    replaced_set_id     text,
    generation          integer NOT NULL DEFAULT 1,
    UNIQUE (user_id, id),
    -- Один первичный набор на пользователя, линию программы и слот (§9).
    UNIQUE (user_id, program_lineage, slot_family_id, generation),
    CHECK ((finalized_at IS NULL) = (finalized_status IS NULL)),
    CHECK ((kind = 'review') = (delay_days IS NOT NULL))
);

ALTER TABLE assignments
    ADD COLUMN assessment_set_id      text,
    ADD COLUMN anchor_snapshot        jsonb,
    ADD COLUMN anchor_accepted_at     timestamptz,
    ADD COLUMN replaces_assignment_id text,
    ADD FOREIGN KEY (user_id, assessment_set_id) REFERENCES assessment_sets (user_id, id) ON DELETE CASCADE,
    ADD FOREIGN KEY (user_id, replaces_assignment_id) REFERENCES assignments (user_id, id) ON DELETE CASCADE,
    ADD CONSTRAINT assignments_set_role CHECK ((role IN ('diagnostic', 'review')) = (assessment_set_id IS NOT NULL)),
    DROP CONSTRAINT assignments_status_check,
    ADD CONSTRAINT assignments_status_check CHECK (status IN ('open', 'completed', 'skipped', 'withdrawn'));
CREATE INDEX assignments_set ON assignments (assessment_set_id) WHERE assessment_set_id IS NOT NULL;

ALTER TABLE content_revisions
    ADD COLUMN withdrawn_at      timestamptz,
    ADD COLUMN withdrawal_reason text;

-- +goose Down
ALTER TABLE content_revisions DROP COLUMN withdrawal_reason, DROP COLUMN withdrawn_at;
DROP INDEX assignments_set;
DELETE FROM assignments WHERE assessment_set_id IS NOT NULL OR status = 'skipped';
ALTER TABLE assignments
    DROP CONSTRAINT assignments_status_check,
    ADD CONSTRAINT assignments_status_check CHECK (status IN ('open', 'completed', 'withdrawn')),
    DROP CONSTRAINT assignments_set_role,
    DROP COLUMN replaces_assignment_id,
    DROP COLUMN anchor_accepted_at,
    DROP COLUMN anchor_snapshot,
    DROP COLUMN assessment_set_id;
DROP TABLE assessment_sets;
