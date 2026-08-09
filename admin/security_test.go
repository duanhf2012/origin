package admin

import (
	"errors"
	"testing"
)

// guardErrorConstant 是测试侧独立的 string 常量类型。const 转换要求其输入也是常量，
// 因此会在 ErrUnauthenticated 或 ErrForbidden 退化为可重赋值 var 时编译失败。
type guardErrorConstant string

const (
	_ guardErrorConstant = guardErrorConstant(ErrUnauthenticated)
	_ guardErrorConstant = guardErrorConstant(ErrForbidden)
)

func TestGuardErrorsAreStableAndMatchThemselves(t *testing.T) {
	if ErrUnauthenticated.Error() != "admin unauthenticated" || ErrForbidden.Error() != "admin forbidden" {
		t.Fatalf("guard errors = %q, %q", ErrUnauthenticated, ErrForbidden)
	}
	if !errors.Is(ErrUnauthenticated, ErrUnauthenticated) || !errors.Is(ErrForbidden, ErrForbidden) {
		t.Fatal("guard sentinels did not match themselves")
	}
}
