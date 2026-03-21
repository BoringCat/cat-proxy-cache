package server

type CtxKey struct {
	name string
}

var (
	UpStreamURL = CtxKey{"UpStreamURL"}
	CacheKey    = CtxKey{"CacheKey"}
	RequestId   = CtxKey{"RequestId"}
)
