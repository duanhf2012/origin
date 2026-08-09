package admin

import (
	"context"
	"errors"
	"net/http"
)

// Principal 表示 Guard 已验证的调用主体。
type Principal struct {
	Subject    string
	Roles      []string
	Attributes map[string]string
}

// Operation 描述 Guard 用于授权决策的目标 Admin 操作。
type Operation struct {
	Method      string
	Endpoint    string
	NodeID      string
	ServiceName string
}

// Guard 在执行 Endpoint 前验证 HTTP 请求并授权给定操作。
type Guard interface {
	Authorize(context.Context, *http.Request, Operation) (Principal, error)
}

var (
	// ErrUnauthenticated 让 HTTP Runtime 稳定映射为 401，且不包含认证材料。
	ErrUnauthenticated = errors.New("admin unauthenticated")
	// ErrForbidden 让 HTTP Runtime 稳定映射为 403，且不包含授权内部原因。
	ErrForbidden = errors.New("admin forbidden")
)
