package reqid

import (
	"context"

	"github.com/go-chi/chi/v5/middleware"
)

func FromContext(ctx context.Context) string { return middleware.GetReqID(ctx) }
