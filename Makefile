CONFIG_VARS := HTTP_ADDR HTTP_READ_TIMEOUT HTTP_READ_HEADER_TIMEOUT HTTP_WRITE_TIMEOUT \
	HTTP_IDLE_TIMEOUT LOG_LEVEL SHUTDOWN_TIMEOUT DATABASE_URL DATABASE_MAX_CONNS \
	DATABASE_MIN_CONNS DATABASE_MAX_CONN_LIFETIME DATABASE_CONNECT_TIMEOUT \
	DATABASE_QUERY_TIMEOUT OTEL_SERVICE_NAME OTEL_RESOURCE_ATTRIBUTES \
	OTEL_EXPORTER_OTLP_ENDPOINT OTEL_EXPORTER_OTLP_PROTOCOL OTEL_TRACES_SAMPLER OTEL_TRACES_SAMPLER_ARG \
	OTEL_EXPORTER_OTLP_TIMEOUT OTEL_BSP_EXPORT_TIMEOUT OTEL_METRIC_EXPORT_INTERVAL \
	OTEL_METRIC_EXPORT_TIMEOUT

$(foreach name,$(CONFIG_VARS),$(eval ENV_ORIGIN_$(name) := $(origin $(name))))
$(foreach name,$(CONFIG_VARS),$(if $(findstring environment,$(ENV_ORIGIN_$(name))),$(eval ENV_VALUE_$(name) := $($(name)))))

include .env.example
-include .env

$(foreach name,$(CONFIG_VARS),$(if $(findstring environment,$(ENV_ORIGIN_$(name))),$(eval $(name) := $(ENV_VALUE_$(name)))))

export

IMAGE ?= trip-service:lab2

.PHONY: build docker-build generate lint migrate migrate-down run test test-integration

build:
	go build ./...

docker-build:
	docker build --tag "$(IMAGE)" .

generate:
	go tool oapi-codegen --config oapi-codegen.yaml contracts/openapi/trip-service.openapi.yaml
	go generate ./internal/...

lint:
	golangci-lint run

migrate:
	go tool goose -dir migrations postgres "$(DATABASE_URL)" up

migrate-down:
	go tool goose -dir migrations postgres "$(DATABASE_URL)" down

run:
	go run ./cmd/trip-service

test:
	go test -race -count=2 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

test-integration:
	go test -race -count=2 -tags=integration ./internal/repository/...
