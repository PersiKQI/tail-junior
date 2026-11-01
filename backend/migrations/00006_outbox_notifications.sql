-- Этап 5 спецификации v0.1 (§18, §21, §22): transactional outbox, единственная установка с
-- push token, настройки напоминаний, задания уведомлений с резервированием квоты,
-- клиентские наблюдения.

-- +goose Up
-- Outbox переносит уже состоявшиеся факты получателю. Не хранит токен, суммы бюджета и
-- заметки: только ссылку на источник (§21).
CREATE TABLE outbox (
    id           text PRIMARY KEY,
    user_id      text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    source_kind  text NOT NULL CHECK (source_kind IN ('fact', 'client_event')),
    source_id    text NOT NULL,
    destination  text NOT NULL CHECK (destination IN ('analytics')),
    state        text NOT NULL CHECK (state IN ('pending', 'processing', 'done', 'dead')),
    attempts     integer NOT NULL DEFAULT 0,
    available_at timestamptz NOT NULL,
    lease_until  timestamptz,
    last_error   text,
    created_at   timestamptz NOT NULL,
    done_at      timestamptz,
    UNIQUE (user_id, destination, source_kind, source_id)
);
CREATE INDEX outbox_ready ON outbox (available_at) WHERE state IN ('pending', 'processing');

-- Одна активная установка на анонимный профиль (§4, §18).
CREATE TABLE devices (
    user_id               text PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    installation_id       text NOT NULL,
    platform              text NOT NULL CHECK (platform IN ('ios', 'android', 'web')),
    provider              text NOT NULL CHECK (provider IN ('expo', 'fcm', 'apns', 'dryrun')),
    push_token_ciphertext bytea,
    push_token_sha256     text,
    permission            text NOT NULL CHECK (permission IN ('granted', 'denied', 'undetermined')),
    updated_at            timestamptz NOT NULL
);

-- Уведомления выключены до явного выбора (§18).
CREATE TABLE reminder_preferences (
    user_id          text PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    revision         integer NOT NULL,
    mode             text NOT NULL CHECK (mode IN ('off', 'three_per_week', 'daily')),
    days_iso         integer[] NOT NULL,
    local_time       text NOT NULL,
    categories       text[] NOT NULL,
    pause_until      date,
    auto_pause_until timestamptz,
    auto_paused_at   timestamptz,
    updated_at       timestamptz NOT NULL
);

-- Жизненный цикл: planned → reserved → submitting → provider_accepted; cancelled, failed,
-- unknown. Квота считается из резервирования, включая unknown (§18).
CREATE TABLE notification_jobs (
    id                   text PRIMARY KEY,
    user_id              text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    trigger_key          text NOT NULL,
    trigger_type         text NOT NULL,
    category             text NOT NULL CHECK (category IN ('learning', 'event', 'streak')),
    template_id          text NOT NULL,
    template_revision    integer NOT NULL,
    target_type          text NOT NULL,
    target_id            text NOT NULL,
    character_id         text NOT NULL,
    state                text NOT NULL CHECK (state IN ('planned', 'reserved', 'submitting', 'provider_accepted', 'cancelled', 'failed', 'unknown')),
    scheduled_at         timestamptz NOT NULL,
    expires_at           timestamptz NOT NULL,
    reserved_at          timestamptz,
    lease_until          timestamptz,
    provider_message_id  text,
    provider_accepted_at timestamptz,
    opened_at            timestamptz,
    attempts             integer NOT NULL DEFAULT 0,
    last_error_code      text,
    created_at           timestamptz NOT NULL,
    UNIQUE (user_id, trigger_key)
);
CREATE INDEX notification_jobs_quota ON notification_jobs (user_id, reserved_at) WHERE reserved_at IS NOT NULL;

-- Клиентские наблюдения: не начисляют награды (§22).
CREATE TABLE client_events (
    user_id            text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_event_id    text NOT NULL,
    name               text NOT NULL,
    client_occurred_at timestamptz NOT NULL,
    received_at        timestamptz NOT NULL,
    target_id          text,
    PRIMARY KEY (user_id, client_event_id)
);

-- +goose Down
DROP TABLE client_events;
DROP TABLE notification_jobs;
DROP TABLE reminder_preferences;
DROP TABLE devices;
DROP TABLE outbox;
