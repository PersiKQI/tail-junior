-- Перенос контракта старого D7 (спецификация §23): окна, открытые до переключения, работают
-- до исходного closesAt. Ответ один и неизменен; история и когорта остаются legacy, в XP,
-- учебные дни и RS07 не попадают.

-- +goose Up
CREATE TABLE legacy_d7_answers (
    user_id         text PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    choice_id       text NOT NULL,
    reason_id       text NOT NULL,
    correct         boolean NOT NULL,
    content_version text NOT NULL,
    accepted_at     timestamptz NOT NULL
);

-- +goose Down
DROP TABLE legacy_d7_answers;
