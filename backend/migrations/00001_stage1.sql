-- Этап 1 спецификации v0.1 (§4–§8, §24): анонимная личность, контентные редакции,
-- назначения и попытки learning/practice, квитанции идемпотентности.
-- Все пользовательские таблицы ссылаются на users с ON DELETE CASCADE (удаление аккаунта, §4);
-- дочерние сущности — составным ключом (user_id, parent_id), чтобы не связать данные разных
-- пользователей (§5).

-- +goose Up
CREATE TABLE users (
    id                 text PRIMARY KEY,
    created_at         timestamptz NOT NULL,
    installation_id    text,
    nickname           text,
    age_group          text CHECK (age_group IN ('14-17', '18-20', 'other')),
    character_id       text CHECK (character_id IN ('lumi', 'spark', 'nova')),
    reduced_motion     boolean NOT NULL DEFAULT false,
    profile_revision   integer NOT NULL DEFAULT 1,
    learning_timezone  text,
    timezone_locked_at timestamptz,
    route              text NOT NULL DEFAULT 'starter' CHECK (route IN ('starter', 'full')),
    analytics_enabled  boolean NOT NULL DEFAULT false
);

-- Хранится SHA-256 UTF-8 строки токена (§4); один активный credential на пользователя.
CREATE TABLE credentials (
    id         text PRIMARY KEY,
    user_id    text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    created_at timestamptz NOT NULL,
    revoked_at timestamptz
);
CREATE UNIQUE INDEX credentials_one_active ON credentials (user_id) WHERE revoked_at IS NULL;

-- Поставка контента. Импорт создаёт draft; публикация — отдельная команда с утверждением (§6).
CREATE TABLE program_releases (
    version      text PRIMARY KEY,
    lineage_id   text NOT NULL,
    status       text NOT NULL CHECK (status IN ('draft', 'published', 'retired')),
    manifest     jsonb NOT NULL,
    sha256       text NOT NULL,
    imported_at  timestamptz NOT NULL,
    published_at timestamptz,
    approved_by  text,
    CHECK ((status = 'draft') = (published_at IS NULL))
);

CREATE TABLE content_families (
    id                  text PRIMARY KEY,
    purpose             text NOT NULL CHECK (purpose IN ('learning', 'diagnostic', 'review')),
    module_id           text NOT NULL,
    topic_tags          text[] NOT NULL,
    assessed_outcome_id text
);

-- Редакция неизменяема: повторный импорт той же редакции с другим содержимым отклоняется.
CREATE TABLE content_revisions (
    item_id          text NOT NULL,
    revision         integer NOT NULL,
    grading_revision integer NOT NULL,
    locale           text NOT NULL,
    family_id        text NOT NULL REFERENCES content_families (id),
    status           text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'withdrawn')),
    public_payload   jsonb NOT NULL,
    private_payload  jsonb NOT NULL,
    payload_sha256   text NOT NULL,
    created_at       timestamptz NOT NULL,
    PRIMARY KEY (item_id, revision, grading_revision, locale)
);

CREATE TABLE release_items (
    program_version  text NOT NULL REFERENCES program_releases (version),
    item_id          text NOT NULL,
    revision         integer NOT NULL,
    grading_revision integer NOT NULL,
    locale           text NOT NULL,
    family_id        text NOT NULL REFERENCES content_families (id),
    PRIMARY KEY (program_version, item_id),
    UNIQUE (program_version, family_id),
    FOREIGN KEY (item_id, revision, grading_revision, locale)
        REFERENCES content_revisions (item_id, revision, grading_revision, locale)
);

CREATE TABLE enrollments (
    user_id         text PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    program_version text NOT NULL REFERENCES program_releases (version),
    enrolled_at     timestamptz NOT NULL,
    cohort_id       text
);

CREATE TABLE assignments (
    id              text PRIMARY KEY,
    user_id         text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    assignment_key  text NOT NULL,
    family_id       text NOT NULL REFERENCES content_families (id),
    item_id         text NOT NULL,
    program_version text NOT NULL REFERENCES program_releases (version),
    role            text NOT NULL CHECK (role IN ('learning', 'practice', 'diagnostic', 'review')),
    practice_origin text CHECK (practice_origin IN ('manual', 'scheduled')),
    due_at          timestamptz,
    streak_eligible boolean NOT NULL DEFAULT false,
    status          text NOT NULL CHECK (status IN ('open', 'completed', 'withdrawn')),
    created_at      timestamptz NOT NULL,
    completed_at    timestamptz,
    UNIQUE (user_id, assignment_key),
    UNIQUE (user_id, id),
    CHECK ((role = 'practice') = (practice_origin IS NOT NULL))
);
-- Одна открытая практика на семейство: повторный запрос возвращает её.
CREATE UNIQUE INDEX assignments_one_open_practice ON assignments (user_id, family_id)
    WHERE role = 'practice' AND status = 'open';

-- Попытка фиксирует редакцию, оценивание, поставку и порядок вариантов при выдаче (§6).
CREATE TABLE attempts (
    id                    text PRIMARY KEY,
    user_id               text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    assignment_id         text NOT NULL,
    family_id             text NOT NULL REFERENCES content_families (id),
    item_id               text NOT NULL,
    revision              integer NOT NULL,
    grading_revision      integer NOT NULL,
    locale                text NOT NULL,
    program_version       text NOT NULL REFERENCES program_releases (version),
    role                  text NOT NULL CHECK (role IN ('learning', 'practice', 'diagnostic', 'review')),
    status                text NOT NULL CHECK (status IN ('started', 'submitted', 'abandoned', 'withdrawn')),
    option_order          jsonb NOT NULL,
    issued_at             timestamptz NOT NULL,
    closed_at             timestamptz,
    independence_eligible boolean NOT NULL DEFAULT true,
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, assignment_id) REFERENCES assignments (user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (item_id, revision, grading_revision, locale)
        REFERENCES content_revisions (item_id, revision, grading_revision, locale),
    CHECK ((status = 'started') = (closed_at IS NULL))
);
CREATE UNIQUE INDEX attempts_one_started ON attempts (assignment_id) WHERE status = 'started';

CREATE TABLE submissions (
    id                  text PRIMARY KEY,
    user_id             text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    attempt_id          text NOT NULL UNIQUE,
    choice_id           text NOT NULL,
    reason_id           text NOT NULL,
    confidence          text CHECK (confidence IN ('sure', 'unsure', 'guess')),
    accepted_at         timestamptz NOT NULL,
    client_completed_at timestamptz,
    UNIQUE (user_id, id),
    FOREIGN KEY (user_id, attempt_id) REFERENCES attempts (user_id, id) ON DELETE CASCADE
);

-- Исходная оценка не переписывается: переоценка — новая строка с replaces_evaluation_id (§6).
CREATE TABLE evaluations (
    id                     text PRIMARY KEY,
    user_id                text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    attempt_id             text NOT NULL,
    submission_id          text NOT NULL,
    grading_revision       integer NOT NULL,
    evaluated_at           timestamptz NOT NULL,
    choice_correct         boolean NOT NULL,
    reason_correct         boolean NOT NULL,
    passed                 boolean NOT NULL,
    feedback               jsonb NOT NULL,
    replaces_evaluation_id text REFERENCES evaluations (id),
    correction_reason      text,
    FOREIGN KEY (user_id, attempt_id) REFERENCES attempts (user_id, id) ON DELETE CASCADE,
    FOREIGN KEY (user_id, submission_id) REFERENCES submissions (user_id, id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX evaluations_one_original ON evaluations (attempt_id) WHERE replaces_evaluation_id IS NULL;

-- Первый принятый ответ по семейству сохраняется навсегда (§8).
CREATE TABLE learning_completions (
    user_id          text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id        text NOT NULL REFERENCES content_families (id),
    first_attempt_id text NOT NULL,
    accepted_at      timestamptz NOT NULL,
    PRIMARY KEY (user_id, family_id),
    FOREIGN KEY (user_id, first_attempt_id) REFERENCES attempts (user_id, id) ON DELETE CASCADE
);

CREATE TABLE assistance_events (
    id          text PRIMARY KEY,
    user_id     text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    attempt_id  text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('hint', 'term')),
    term_id     text,
    provided_at timestamptz NOT NULL,
    FOREIGN KEY (user_id, attempt_id) REFERENCES attempts (user_id, id) ON DELETE CASCADE
);

CREATE TABLE content_exposures (
    id          text PRIMARY KEY,
    user_id     text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    family_id   text NOT NULL REFERENCES content_families (id),
    item_id     text NOT NULL,
    attempt_id  text NOT NULL,
    kind        text NOT NULL CHECK (kind IN ('card', 'explanation')),
    provided_at timestamptz NOT NULL,
    FOREIGN KEY (user_id, attempt_id) REFERENCES attempts (user_id, id) ON DELETE CASCADE
);
CREATE INDEX content_exposures_family ON content_exposures (user_id, family_id);

-- Квитанции успешных мутаций хранятся весь срок жизни профиля (§21).
CREATE TABLE idempotency_receipts (
    user_id                  text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key                      text NOT NULL,
    method                   text NOT NULL,
    route                    text NOT NULL,
    request_path             text NOT NULL,
    canonical_request_sha256 text NOT NULL,
    http_status              integer NOT NULL,
    public_response          jsonb NOT NULL,
    created_at               timestamptz NOT NULL,
    PRIMARY KEY (user_id, key)
);

-- +goose Down
DROP TABLE idempotency_receipts;
DROP TABLE content_exposures;
DROP TABLE assistance_events;
DROP TABLE learning_completions;
DROP TABLE evaluations;
DROP TABLE submissions;
DROP TABLE attempts;
DROP TABLE assignments;
DROP TABLE enrollments;
DROP TABLE release_items;
DROP TABLE content_revisions;
DROP TABLE content_families;
DROP TABLE program_releases;
DROP TABLE credentials;
DROP TABLE users;
