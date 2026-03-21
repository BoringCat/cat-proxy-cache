package utils

import (
	"iter"
)

func CopyMap[K comparable, V any](dst map[K]V, src map[K]V) {
	for k, v := range src {
		dst[k] = v
	}
}

func OrderValue[T any](values ...*T) *T {
	for _, val := range values {
		if val != nil {
			return val
		}
	}
	return nil
}

type Int interface {
	int | int8 | int16 | int32 | int64
}
type Uint interface {
	uint | uint8 | uint16 | uint32 | uint64
}
type Float interface {
	float32 | float64
}
type Number interface {
	Int | Uint | Float
}

func AddRange[T Number](start, end T, add T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for this := start; this <= end; this += add {
			if !yield(this) {
				return
			}
		}
	}
}

func MultipRange[T Number](start, end T, multip T) iter.Seq[T] {
	return func(yield func(T) bool) {
		for this := start; this <= end; this *= multip {
			if !yield(this) {
				return
			}
		}
	}
}
