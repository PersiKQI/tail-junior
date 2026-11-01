.PHONY: test test-db test-pg test-race vet run seed

DB_URL ?= postgres://postgres:postgres@127.0.0.1:55433/postgres?sslmode=disable

# Юнит-тесты; интеграционные без TEST_DATABASE_URL пропускаются.
test:
	go test ./...

# Поднимает PostgreSQL из docker-compose.yml и ждёт healthcheck.
test-db:
	docker compose up -d --wait postgres

# Все тесты на настоящем PostgreSQL (нужен make test-db): каждый тест создаёт и удаляет свою базу.
test-pg: export TEST_DATABASE_URL ?= $(DB_URL)
test-pg:
	go test -count=1 ./...

test-race: export TEST_DATABASE_URL ?= $(DB_URL)
test-race:
	go test -race -count=1 ./...

vet:
	go vet ./...

# Импорт и публикация встроенного комплекта в локальную базу. approved-by фиксирует, кто
# утвердил поставку; для локальной разработки — local-dev, для пилота — имя редактора.
seed: export TEYL_DATABASE_URL ?= $(DB_URL)
seed:
	go run ./cmd/teylctl migrate
	go run ./cmd/teylctl import
	go run ./cmd/teylctl publish -version teyl-learning-2026-11-01-draft-1 -approved-by local-dev
	go run ./cmd/teylctl publish-lab -case lab-LB01 -revision 1 -approved-by local-dev
	go run ./cmd/teylctl publish-challenge -family evt-money-early -revision 1 -approved-by local-dev

# API и worker в одном процессе; уведомления — только dry-run в журнал.
run: export TEYL_DATABASE_URL ?= $(DB_URL)
run:
	go run ./cmd/teylapi -migrate -mode all
