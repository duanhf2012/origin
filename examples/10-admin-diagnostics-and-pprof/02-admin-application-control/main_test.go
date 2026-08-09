package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/duanhf2012/origin/v3/admin"
	"github.com/duanhf2012/origin/v3/errs"
)

// TestApplicationEndpoints 验证 Application Endpoint 的严格输入和并发安全状态外观。
func TestApplicationEndpoints(t *testing.T) {
	state := newControlState()
	endpoints := applicationEndpoints(state)
	status := endpoints[0]
	reload := endpoints[1]

	response, err := status.Invoke(t.Context(), admin.NewRequest("get", admin.Principal{}, nil, nil, nil))
	if err != nil || response.Status() != http.StatusOK {
		t.Fatalf("status response = %d, %v", response.Status(), err)
	}
	var initial applicationStatus
	if err := json.Unmarshal(response.Body(), &initial); err != nil || initial.RoutingRevision != 1 {
		t.Fatalf("initial status = %+v, %v", initial, err)
	}

	response, err = reload.Invoke(
		context.Background(),
		admin.NewRequest("post", admin.Principal{}, nil, nil, []byte(`{"routing_revision":2}`)),
	)
	if err != nil || response.Status() != http.StatusNoContent || state.routingRevision.Load() != 2 {
		t.Fatalf("reload response = %d, %v, revision=%d", response.Status(), err, state.routingRevision.Load())
	}

	_, err = reload.Invoke(
		context.Background(),
		admin.NewRequest("invalid", admin.Principal{}, nil, nil, []byte(`{"routing_revision":3,"unknown":true}`)),
	)
	if !errs.IsCode(err, errs.CodeInvalidArgument) || state.routingRevision.Load() != 2 {
		t.Fatalf("invalid reload error = %v, revision=%d", err, state.routingRevision.Load())
	}
}
