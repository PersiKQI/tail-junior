-- Этап 4 спецификации v0.1 (§14, §16, §19, §23): денежный план и журналы, игровое событие,
-- косметика персонажа, перенос данных текущего приложения. Все суммы — копейки (Minor).

-- +goose Up
ALTER TABLE users ADD COLUMN equipment_revision integer NOT NULL DEFAULT 1;

-- Бюджет и текущая цель — один согласованный снимок с revision (§19).
CREATE TABLE money_plans (
    user_id                    text PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    revision                   integer NOT NULL,
    income_minor               bigint NOT NULL CHECK (income_minor BETWEEN 0 AND 100000000),
    reserve_minor              bigint NOT NULL CHECK (reserve_minor BETWEEN 0 AND 100000000),
    monthly_contribution_minor bigint NOT NULL CHECK (monthly_contribution_minor BETWEEN 0 AND 100000000),
    food_minor                 bigint NOT NULL CHECK (food_minor BETWEEN 0 AND 100000000),
    transport_minor            bigint NOT NULL CHECK (transport_minor BETWEEN 0 AND 100000000),
    housing_minor              bigint NOT NULL CHECK (housing_minor BETWEEN 0 AND 100000000),
    other_minor                bigint NOT NULL CHECK (other_minor BETWEEN 0 AND 100000000),
    updated_at                 timestamptz NOT NULL
);

CREATE TABLE goals (
    id           text PRIMARY KEY,
    user_id      text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    title        text NOT NULL,
    target_minor bigint NOT NULL CHECK (target_minor BETWEEN 1 AND 100000000),
    saved_minor  bigint NOT NULL CHECK (saved_minor BETWEEN 0 AND 100000000),
    months       integer NOT NULL CHECK (months BETWEEN 1 AND 120),
    active       boolean NOT NULL,
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,
    UNIQUE (user_id, id)
);
CREATE UNIQUE INDEX goals_one_active ON goals (user_id) WHERE active;

-- Самоотчёты: не банковский перевод и не учебное действие (§19).
CREATE TABLE checkins (
    id              text PRIMARY KEY,
    user_id         text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    goal_id         text NOT NULL,
    kind            text NOT NULL CHECK (kind IN ('saved', 'reviewed')),
    amount_minor    bigint CHECK (amount_minor BETWEEN 1 AND 100000000),
    reviewed_fields text[] NOT NULL DEFAULT '{}',
    note            text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL,
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, goal_id) REFERENCES goals (user_id, id) ON DELETE CASCADE,
    CHECK ((kind = 'saved') = (amount_minor IS NOT NULL))
);

CREATE TABLE expenses (
    id           text PRIMARY KEY,
    user_id      text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    revision     integer NOT NULL,
    amount_minor bigint NOT NULL CHECK (amount_minor BETWEEN 1 AND 100000000),
    category_id  text NOT NULL CHECK (category_id IN ('food', 'transport', 'housing', 'other')),
    occurred_on  date NOT NULL,
    note         text NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL,
    voided_at    timestamptz
);
CREATE INDEX expenses_page ON expenses (user_id, occurred_on DESC, created_at DESC, id DESC);

CREATE TABLE planned_actions (
    id              text PRIMARY KEY,
    user_id         text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    goal_id         text NOT NULL,
    kind            text NOT NULL CHECK (kind IN ('save', 'review')),
    amount_minor    bigint CHECK (amount_minor BETWEEN 1 AND 100000000),
    note            text NOT NULL DEFAULT '',
    due_on          date NOT NULL,
    status          text NOT NULL CHECK (status IN ('pending', 'completed', 'cancelled')),
    checkin_id      text UNIQUE,
    created_at      timestamptz NOT NULL,
    completed_at    timestamptz,
    cancelled_at    timestamptz,
    FOREIGN KEY (user_id, goal_id) REFERENCES goals (user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, checkin_id) REFERENCES checkins (user_id, id) ON DELETE CASCADE,
    CHECK ((kind = 'save') = (amount_minor IS NOT NULL)),
    CHECK ((status = 'completed') = (checkin_id IS NOT NULL))
);
CREATE INDEX planned_actions_page ON planned_actions (user_id, status, due_on);

-- Игровое событие поверх лабораторного кейса (§16).
CREATE TABLE challenge_releases (
    family_id      text NOT NULL,
    revision       integer NOT NULL,
    starts_at      timestamptz NOT NULL,
    ends_at        timestamptz NOT NULL,
    status         text NOT NULL CHECK (status IN ('draft', 'published', 'withdrawn')),
    case_family_id text NOT NULL,
    cosmetic_id    text NOT NULL,
    payload        jsonb NOT NULL,
    sha256         text NOT NULL,
    imported_at    timestamptz NOT NULL,
    published_at   timestamptz,
    approved_by    text,
    PRIMARY KEY (family_id, revision),
    CHECK (ends_at > starts_at)
);

-- Одно участие на семейство события: перезапуск и новая revision награду не умножают.
CREATE TABLE challenge_participations (
    id            text PRIMARY KEY,
    user_id       text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id     text NOT NULL,
    revision      integer NOT NULL,
    experiment_id text NOT NULL,
    joined_at     timestamptz NOT NULL,
    completed_at  timestamptz,
    UNIQUE (user_id, family_id),
    FOREIGN KEY (family_id, revision) REFERENCES challenge_releases (family_id, revision),
    FOREIGN KEY (user_id, experiment_id) REFERENCES lab_experiments (user_id, id) ON DELETE CASCADE
);

-- Открытый инвентарь и текущий выбор отдельны (§14).
CREATE TABLE cosmetic_inventory (
    user_id     text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    cosmetic_id text NOT NULL,
    source_key  text NOT NULL,
    unlocked_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, cosmetic_id)
);

CREATE TABLE cosmetic_equipment (
    user_id     text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    slot        text NOT NULL,
    cosmetic_id text NOT NULL,
    PRIMARY KEY (user_id, slot)
);

-- Перенос из SQLite: исходные записи с версией и ограничениями, без выдуманной истории (§23).
CREATE TABLE legacy_records (
    id             text PRIMARY KEY,
    user_id        text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source_kind    text NOT NULL,
    source_id      text NOT NULL,
    source_version text,
    imported_at    timestamptz NOT NULL,
    payload        jsonb NOT NULL,
    limitations    text NOT NULL,
    source_sha256  text NOT NULL,
    UNIQUE (user_id, source_kind, source_id)
);

CREATE TABLE import_manifests (
    id             text PRIMARY KEY,
    source_sha256  text NOT NULL,
    backup_path    text NOT NULL,
    imported_at    timestamptz NOT NULL,
    schema_version integer NOT NULL,
    counts         jsonb NOT NULL
);

-- +goose Down
DROP TABLE import_manifests;
DROP TABLE legacy_records;
DROP TABLE cosmetic_equipment;
DROP TABLE cosmetic_inventory;
DROP TABLE challenge_participations;
DROP TABLE challenge_releases;
DROP TABLE planned_actions;
DROP TABLE expenses;
DROP TABLE checkins;
DROP TABLE goals;
DROP TABLE money_plans;
ALTER TABLE users DROP COLUMN equipment_revision;
