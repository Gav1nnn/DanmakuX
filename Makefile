APP=danmakux

.PHONY: deps up down reset-db run build test fmt load-test k6-load

deps:
	go mod tidy

up:
	docker compose up -d

down:
	docker compose down

reset-db:
	docker compose down -v
	docker compose up -d

run:
	go run ./cmd/$(APP)

build:
	go build -o bin/$(APP) ./cmd/$(APP)

test:
	go test ./...

fmt:
	gofmt -w ./cmd ./internal ./pkg ./scripts

load-test:
	go run ./scripts/load/ws_load.go $(ARGS)

k6-load:
	k6 run $(ARGS) ./scripts/k6/danmaku.js
