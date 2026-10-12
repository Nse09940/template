# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS build

ARG TARGETOS=linux
ARG TARGETARCH

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/trip-service ./cmd/trip-service

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build --chown=nonroot:nonroot /out/trip-service /trip-service

USER nonroot:nonroot
EXPOSE 8080

ENTRYPOINT ["/trip-service"]
