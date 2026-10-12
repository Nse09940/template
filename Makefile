CONFIG_VARS := HTTP_ADDR HTTP_READ_TIMEOUT HTTP_READ_HEADER_TIMEOUT HTTP_WRITE_TIMEOUT \
	HTTP_IDLE_TIMEOUT LOG_LEVEL SHUTDOWN_TIMEOUT DATABASE_URL DATABASE_MAX_CONNS \
	DATABASE_MIN_CONNS DATABASE_MAX_CONN_LIFETIME DATABASE_CONNECT_TIMEOUT \
	DATABASE_QUERY_TIMEOUT

$(foreach name,$(CONFIG_VARS),$(eval ENV_ORIGIN_$(name) := $(origin $(name))))
$(foreach name,$(CONFIG_VARS),$(if $(findstring environment,$(ENV_ORIGIN_$(name))),$(eval ENV_VALUE_$(name) := $($(name)))))

include .env.example
-include .env

$(foreach name,$(CONFIG_VARS),$(if $(findstring environment,$(ENV_ORIGIN_$(name))),$(eval $(name) := $(ENV_VALUE_$(name)))))

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
