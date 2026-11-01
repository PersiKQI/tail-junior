-- Этап 3 спецификации v0.1 (§10–§15): учебная лаборатория V2, доменные факты, XP, бейджи,
-- учебные свидетельства, учебные дни и защита серии. XP, бейджи и дни — разные журналы;
-- уникальные бизнес-основания защищают начисления даже при другом Idempotency-Key (§21).

-- +goose Up
CREATE TABLE lab_models (
    version     text PRIMARY KEY,
    payload     jsonb NOT NULL,
    sha256      text NOT NULL,
    imported_at timestamptz NOT NULL
);

CREATE TABLE lab_cases (
    family_id       text NOT NULL,
    revision        integer NOT NULL,
    model_version   text NOT NULL REFERENCES lab_models (version),
    bundle_revision integer NOT NULL,
    payload         jsonb NOT NULL,
    sha256          text NOT NULL,
    status          text NOT NULL CHECK (status IN ('draft', 'published', 'retired')),
    imported_at     timestamptz NOT NULL,
    published_at    timestamptz,
    approved_by     text,
    PRIMARY KEY (family_id, revision)
);

-- Эксперимент закрепляет редакцию кейса, пакет сценариев, модель и капитал (§10).
CREATE TABLE lab_experiments (
    id                        text PRIMARY KEY,
    user_id                   text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    case_family_id            text NOT NULL,
    case_revision             integer NOT NULL,
    model_version             text NOT NULL REFERENCES lab_models (version),
    bundle_revision           integer NOT NULL,
    initial_minor             bigint NOT NULL CHECK (initial_minor > 0),
    status                    text NOT NULL CHECK (status IN ('open', 'completed')),
    created_at                timestamptz NOT NULL,
    completed_at              timestamptz,
    comparison_outcome        text CHECK (comparison_outcome IN ('keep', 'revise')),
    comparison_reason_id      text,
    comparison_reason_status  text,
    comparison_alternative_id text,
    compared_at               timestamptz,
    UNIQUE (user_id, id),
    FOREIGN KEY (case_family_id, case_revision) REFERENCES lab_cases (family_id, revision),
    CHECK ((status = 'completed') = (compared_at IS NOT NULL)),
    CHECK ((comparison_outcome = 'revise') = (comparison_alternative_id IS NOT NULL))
);

-- Решение неизменяемо: альтернатива — новая строка со ссылкой на исходное (§10).
CREATE TABLE lab_decisions (
    id                 text PRIMARY KEY,
    user_id            text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    experiment_id      text NOT NULL,
    source_decision_id text,
    cash_units         integer NOT NULL CHECK (cash_units BETWEEN 0 AND 10),
    bond_units         integer NOT NULL CHECK (bond_units BETWEEN 0 AND 10),
    stock_units        integer NOT NULL CHECK (stock_units BETWEEN 0 AND 10),
    fund_units         integer NOT NULL CHECK (fund_units BETWEEN 0 AND 10),
    reason_id          text NOT NULL,
    note               text,
    constraint_status  text NOT NULL CHECK (constraint_status IN ('met', 'reserve_violated')),
    reason_status      text NOT NULL CHECK (reason_status IN ('supported', 'misconception')),
    committed_at       timestamptz NOT NULL,
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, experiment_id) REFERENCES lab_experiments (user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, source_decision_id) REFERENCES lab_decisions (user_id, id) ON DELETE CASCADE,
    CHECK (cash_units + bond_units + stock_units + fund_units = 10)
);
CREATE UNIQUE INDEX lab_decisions_one_original ON lab_decisions (experiment_id) WHERE source_decision_id IS NULL;

-- Один исходный run на decision и сценарий; старый результат не пересчитывается (§11).
CREATE TABLE lab_runs (
    id             text PRIMARY KEY,
    user_id        text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    experiment_id  text NOT NULL,
    decision_id    text NOT NULL,
    scenario_id    text NOT NULL,
    model_version  text NOT NULL REFERENCES lab_models (version),
    input_snapshot jsonb NOT NULL,
    input_sha256   text NOT NULL,
    result         jsonb NOT NULL,
    ending_minor   bigint NOT NULL,
    profit_minor   bigint NOT NULL,
    created_at     timestamptz NOT NULL,
    UNIQUE (user_id, id),
    UNIQUE (decision_id, scenario_id),
    FOREIGN KEY (user_id, experiment_id) REFERENCES lab_experiments (user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, decision_id) REFERENCES lab_decisions (user_id, id) ON DELETE CASCADE
);

CREATE TABLE lab_reflections (
    id             text PRIMARY KEY,
    user_id        text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    run_id         text NOT NULL UNIQUE,
    choice_id      text NOT NULL,
    reason_id      text NOT NULL,
    choice_correct boolean NOT NULL,
    reason_correct boolean NOT NULL,
    passed         boolean NOT NULL,
    feedback       jsonb NOT NULL,
    accepted_at    timestamptz NOT NULL,
    FOREIGN KEY (user_id, run_id) REFERENCES lab_runs (user_id, id) ON DELETE CASCADE
);

-- Состоявшиеся доменные факты: источник наград и будущего outbox. Сырые клиентские события
-- источником наград не являются (§5, §22).
CREATE TABLE domain_facts (
    id             text PRIMARY KEY,
    user_id        text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    name           text NOT NULL,
    schema_version integer NOT NULL,
    source_type    text NOT NULL,
    source_id      text NOT NULL,
    dedupe_key     text NOT NULL,
    safe_payload   jsonb NOT NULL,
    occurred_at    timestamptz NOT NULL,
    UNIQUE (user_id, dedupe_key)
);

-- Журнал XP: исправление — компенсирующая запись с причиной, а не удаление (§13).
CREATE TABLE xp_ledger (
    id               text PRIMARY KEY,
    user_id          text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    basis_key        text NOT NULL,
    amount           integer NOT NULL,
    source_fact_id   text NOT NULL REFERENCES domain_facts (id) ON DELETE CASCADE,
    rule_version     text NOT NULL,
    granted_at       timestamptz NOT NULL,
    compensation_for text REFERENCES xp_ledger (id),
    reason           text,
    UNIQUE (user_id, basis_key)
);

CREATE TABLE badge_grants (
    user_id            text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    badge_id           text NOT NULL,
    rule_version       text NOT NULL,
    granted_at         timestamptz NOT NULL,
    confirmation       text NOT NULL CHECK (confirmation IN ('participation', 'practice', 'evidence')),
    confirmed_later_at timestamptz,
    confirmation_status text NOT NULL DEFAULT 'valid' CHECK (confirmation_status IN ('valid', 'corrected', 'invalidated')),
    source_refs        jsonb NOT NULL,
    PRIMARY KEY (user_id, badge_id)
);

CREATE TABLE learning_evidence (
    id                   text PRIMARY KEY,
    user_id              text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    outcome_id           text NOT NULL,
    attempt_id           text NOT NULL UNIQUE,
    evaluation_id        text NOT NULL,
    assessment_set_id    text NOT NULL,
    actual_delay_seconds bigint,
    accepted_at          timestamptz NOT NULL,
    status               text NOT NULL DEFAULT 'valid' CHECK (status IN ('valid', 'corrected', 'invalidated')),
    rule_version         text NOT NULL,
    FOREIGN KEY (user_id, attempt_id) REFERENCES attempts (user_id, id) ON DELETE CASCADE
);

-- Один учебный день на локальную дату; protected — оплачен зарядом защиты (§15).
CREATE TABLE study_days (
    user_id           text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    local_date        date NOT NULL,
    kind              text NOT NULL CHECK (kind IN ('active', 'protected')),
    calendar_timezone text NOT NULL,
    source_fact_id    text REFERENCES domain_facts (id) ON DELETE CASCADE,
    created_at        timestamptz NOT NULL,
    PRIMARY KEY (user_id, local_date),
    CHECK ((kind = 'active') = (source_fact_id IS NOT NULL))
);

CREATE TABLE freeze_ledger (
    id         text PRIMARY KEY,
    user_id    text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    basis_key  text NOT NULL,
    delta      integer NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (user_id, basis_key)
);

-- +goose Down
DROP TABLE freeze_ledger;
DROP TABLE study_days;
DROP TABLE learning_evidence;
DROP TABLE badge_grants;
DROP TABLE xp_ledger;
DROP TABLE domain_facts;
DROP TABLE lab_reflections;
DROP TABLE lab_runs;
DROP TABLE lab_decisions;
DROP TABLE lab_experiments;
DROP TABLE lab_cases;
DROP TABLE lab_models;
