//go:generate go run go.uber.org/mock/mockgen -source=contract.go -destination=mocks_test.go -package=http_test
package http

import "context"

type pinger interface{ Ping(context.Context) error }
