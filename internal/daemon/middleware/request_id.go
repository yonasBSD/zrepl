package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"

	"github.com/dsh2dsh/zrepl/internal/daemon/logging"
)

type ctxKeyRequestId struct{}

var (
	RequestIdKey  ctxKeyRequestId = struct{}{}
	nextRequestId atomic.Uint64
)

func RequestId(next http.Handler) http.Handler {
	fn := func(w http.ResponseWriter, r *http.Request) {
		requestId := nextRequestId.Add(1)
		ctx := context.WithValue(r.Context(),
			RequestIdKey, strconv.FormatUint(requestId, 10))
		ctx = logging.WithLogger(ctx, getLogger(r).With(
			slog.Uint64("rid", requestId)))
		next.ServeHTTP(w, r.WithContext(ctx))
	}
	return http.HandlerFunc(fn)
}

func RequestIdFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if id, ok := ctx.Value(RequestIdKey).(string); ok {
		return id
	}
	return ""
}
