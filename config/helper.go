package config

import "unsafe"

type marshalMap[K, V comparable] struct {
	marshal   map[V]K
	unmarshal map[K]V
}

func (m *marshalMap[K, V]) Marshal(val V) (key K, ok bool) {
	key, ok = m.marshal[val]
	return
}
func (m *marshalMap[K, V]) Unmarshal(key K) (val V, ok bool) {
	val, ok = m.unmarshal[key]
	return
}

func NewMarshalMap[K, V comparable](maps map[K]V) *marshalMap[K, V] {
	resp := marshalMap[K, V]{
		marshal:   make(map[V]K),
		unmarshal: make(map[K]V),
	}
	for k, v := range maps {
		resp.marshal[v] = k
		resp.unmarshal[k] = v
	}
	return &resp
}

func byte32(s []byte) (a *[32]byte) {
	if len(a) <= len(s) {
		a = (*[len(a)]byte)(unsafe.Pointer(&s[0]))
	}
	return a
}
