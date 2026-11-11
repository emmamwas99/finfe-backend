.PHONY: dev build test race vet fmt sqlc migrate-up migrate-down up down
dev:
	go run ./cmd/api
build:
	go build ./cmd/api
test:
	go test ./...
race:
	go test -race ./...
vet:
	go vet ./...
fmt:
	go fmt ./...
sqlc:
	sqlc generate
migrate-up:
	go run ./cmd/migrate up
migrate-down:
	go run ./cmd/migrate down
up:
	docker compose up --build
down:
	docker compose down
