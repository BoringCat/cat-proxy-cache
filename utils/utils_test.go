package utils

import (
	"slices"
	"testing"
)

func TestRange(t *testing.T) {
	t.Run("简单加算", func(t *testing.T) {
		resp := slices.Collect(AddRange(16, 128, 16))
		if resp == nil {
			t.Fatal("nil")
		}
		if len(resp) != 8 {
			t.Fatalf("长度不一致: 期望 %d, 得到 %d", 8, len(resp))
		}
	})
	t.Run("简单乘算", func(t *testing.T) {
		resp := slices.Collect(MultipRange(1, 128, 2))
		if resp == nil {
			t.Fatal("nil")
		}
		if len(resp) != 8 {
			t.Fatalf("长度不一致: 期望 %d, 得到 %d", 8, len(resp))
		}
	})
}
