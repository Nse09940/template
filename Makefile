include .env.example
-include .env
export

IMAGE ?= trip-service:lab1

.PHONY: build docker-build generate lint migrate migrate-down run test

build:
	go build ./...

docker-build:
	docker build --tag "$(IMAGE)" .

generate:
	go tool oapi-codegen --config oapi-codegen.yaml contracts/openapi/trip-service.openapi.yaml

lint:
	go vet ./...

migrate:
	go tool goose -dir migrations postgres "$(DATABASE_URL)" up

migrate-down:
	go tool goose -dir migrations postgres "$(DATABASE_URL)" down

run:
	go run ./cmd/trip-service

test:
	go test -race ./...
