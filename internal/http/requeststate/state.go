package requeststate

import (
	"context"
	"net/http"
)

type (
	key   struct{}
	State struct {
		Err     error
		Request *http.Request
	}
)

func With(ctx context.Context, r *http.Request) (context.Context, *State) {
	s := &State{Request: r}
	return context.WithValue(ctx, key{}, s), s
}

func SetError(ctx context.Context, err error) {
	if s, ok := ctx.Value(key{}).(*State); ok {
		s.Err = err
	}
}
