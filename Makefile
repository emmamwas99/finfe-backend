.PHONY: dev build test race vet fmt mongo-setup up down
dev:
	./scripts/dev.sh
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
mongo-setup:
	go run ./cmd/mongo-setup
up:
	docker compose up --build
down:
	docker compose down
