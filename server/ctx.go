package server

import (
	"context"
	"net/url"
)

type CtxKey struct {
	name string
}

var (
	UpStreamURL = CtxKey{"UpStreamURL"}
	CacheKey    = CtxKey{"CacheKey"}
	RequestId   = CtxKey{"RequestId"}
)

func getUpStream(ctx context.Context) (uri *url.URL, ok bool) {
	uri, ok = ctx.Value(UpStreamURL).(*url.URL)
	return
}
func getCacheKey(ctx context.Context) (key string, ok bool) {
	key, ok = ctx.Value(CacheKey).(string)
	return
}
func getRequestId(ctx context.Context) (id string, ok bool) {
	id, ok = ctx.Value(RequestId).(string)
	return
}

func setUpStream(ctx context.Context, uri *url.URL) context.Context {
	return context.WithValue(ctx, UpStreamURL, uri)
}
func setCacheKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, CacheKey, key)
}
func setRequestId(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, RequestId, id)
}
