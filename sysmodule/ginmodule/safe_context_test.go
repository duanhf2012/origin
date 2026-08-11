package ginmodule

import (
	"bytes"
	"context"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSafeContextRequestAccessorsOwnSnapshots(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/players/7?source=test&empty=", bytes.NewBufferString(`{"name":"origin"}`))
	request.Header.Set("X-Request-ID", "request-1")
	taskContext := context.WithValue(context.Background(), safeContextTestKey{}, "trace")
	request = request.WithContext(taskContext)
	ctx := &SafeContext{
		ctx:        taskContext,
		request:    request,
		body:       []byte(`{"name":"origin"}`),
		params:     gin.Params{{Key: "id", Value: "7"}},
		keys:       map[any]any{"principal": "player"},
		clientIP:   "127.0.0.1",
		fullPath:   "/players/:id",
		statusCode: http.StatusOK,
		header:     make(http.Header),
	}
	if ctx.Context().Value(safeContextTestKey{}) != "trace" || ctx.Request() != request ||
		ctx.Param("id") != "7" || ctx.Query("source") != "test" ||
		ctx.GetHeader("X-Request-ID") != "request-1" || ctx.ClientIP() != "127.0.0.1" ||
		ctx.FullPath() != "/players/:id" || ctx.MustGet("principal") != "player" {
		t.Fatalf("SafeContext accessors returned unexpected values")
	}
	if value, exists := ctx.GetQuery("empty"); !exists || value != "" {
		t.Fatalf("empty query value=%q exists=%v", value, exists)
	}
	if _, exists := ctx.GetQuery("missing"); exists {
		t.Fatal("missing query reported as present")
	}
	if _, exists := ctx.Get("missing"); exists {
		t.Fatal("missing key reported as present")
	}
	raw, err := ctx.GetRawData()
	if err != nil || string(raw) != `{"name":"origin"}` {
		t.Fatalf("GetRawData()=%q error=%v", raw, err)
	}
	raw[0] = 'x'
	if string(ctx.body) != `{"name":"origin"}` {
		t.Fatal("GetRawData exposed internal body")
	}
	var body struct {
		Name string `json:"name" binding:"required"`
	}
	if err := ctx.ShouldBindJSON(&body); err != nil || body.Name != "origin" {
		t.Fatalf("ShouldBindJSON() body=%+v error=%v", body, err)
	}
	if err := ctx.ShouldBindJSON(nil); err == nil {
		t.Fatal("ShouldBindJSON(nil) succeeded")
	}
}

type safeContextTestKey struct{}

func TestSnapshotRequestOwnsRequestMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/players/7", bytes.NewBufferString("body"))
	request.Header.Set("X-Test", "before")
	ginContext.Request = request
	ginContext.Params = gin.Params{{Key: "id", Value: "7"}}
	ginContext.Set("principal", "player-7")

	snapshot, err := snapshotRequest(ginContext)
	if err != nil {
		t.Fatal(err)
	}
	ginContext.Params[0].Value = "changed"
	ginContext.Set("principal", "changed")
	request.Header.Set("X-Test", "changed")

	if snapshot.params.ByName("id") != "7" || snapshot.keys["principal"] != "player-7" ||
		snapshot.request.Header.Get("X-Test") != "before" || string(snapshot.body) != "body" {
		t.Fatalf("snapshot changed: params=%v keys=%v header=%q body=%q",
			snapshot.params, snapshot.keys, snapshot.request.Header.Get("X-Test"), snapshot.body)
	}
	if snapshot.request.GetBody == nil {
		t.Fatal("request snapshot did not provide GetBody")
	}
	body, err := snapshot.request.GetBody()
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil || string(got) != "body" {
		t.Fatalf("GetBody()=%q error=%v", got, err)
	}
	if _, err := snapshotRequest(nil); err == nil {
		t.Fatal("nil Gin context was accepted")
	}
}

func TestSafeContextMustGetPanicsForMissingKey(t *testing.T) {
	ctx := &SafeContext{}
	defer func() {
		if recover() == nil {
			t.Fatal("MustGet did not panic")
		}
	}()
	ctx.MustGet("missing")
}

func TestSafeMiddlewareNextAbortAndResponseContracts(t *testing.T) {
	var order []string
	ctx := &SafeContext{
		ctx:        context.Background(),
		request:    httptest.NewRequest(http.MethodGet, "/", nil),
		statusCode: http.StatusOK,
		header:     make(http.Header),
		handler: func(ctx *SafeContext) {
			order = append(order, "handler")
			ctx.Status(http.StatusNoContent)
		},
		middleware: []SafeMiddlewareFunc{
			func(ctx *SafeContext) {
				order = append(order, "first-before")
				ctx.Next()
				order = append(order, "first-after")
			},
			func(ctx *SafeContext) {
				order = append(order, "second-before")
				ctx.Next()
				order = append(order, "second-after")
			},
		},
		index: -1,
	}
	ctx.run()
	want := []string{"first-before", "second-before", "handler", "second-after", "first-after"}
	if len(order) != len(want) {
		t.Fatalf("order=%v", order)
	}
	for index := range want {
		if order[index] != want[index] {
			t.Fatalf("order=%v want=%v", order, want)
		}
	}
	if ctx.statusCode != http.StatusNoContent || ctx.IsAborted() {
		t.Fatalf("status=%d aborted=%v", ctx.statusCode, ctx.IsAborted())
	}

	aborted := &SafeContext{
		ctx:        context.Background(),
		request:    httptest.NewRequest(http.MethodGet, "/", nil),
		statusCode: http.StatusOK,
		header:     make(http.Header),
		handler:    func(*SafeContext) { t.Fatal("aborted handler ran") },
		middleware: []SafeMiddlewareFunc{func(ctx *SafeContext) {
			ctx.Abort()
			ctx.Next()
		}},
		index: -1,
	}
	aborted.run()
	if !aborted.IsAborted() {
		t.Fatal("Abort state not retained")
	}

	finalCallsNext := &SafeContext{
		ctx:        context.Background(),
		request:    httptest.NewRequest(http.MethodGet, "/", nil),
		statusCode: http.StatusOK,
		header:     make(http.Header),
		handler:    func(ctx *SafeContext) { ctx.Next() },
	}
	finalCallsNext.run()
	if finalCallsNext.responseErr == nil {
		t.Fatal("final Handler Next() was accepted")
	}
}

func TestSafeContextRejectsInvalidRenderingSequence(t *testing.T) {
	newContext := func() *SafeContext {
		return &SafeContext{statusCode: http.StatusOK, header: make(http.Header)}
	}

	ctx := newContext()
	ctx.JSON(http.StatusOK, math.Inf(1))
	if ctx.responseErr == nil || !ctx.aborted {
		t.Fatal("JSON encoding error was not retained")
	}

	ctx = newContext()
	ctx.String(http.StatusOK, "%s", "first")
	ctx.Data(http.StatusOK, "text/plain", []byte("second"))
	if ctx.responseErr == nil {
		t.Fatal("multiple render was accepted")
	}

	ctx = newContext()
	ctx.String(http.StatusOK, "body")
	ctx.Header("X-Late", "value")
	if ctx.responseErr == nil {
		t.Fatal("Header after render was accepted")
	}

	ctx = newContext()
	ctx.String(http.StatusOK, "body")
	ctx.Status(http.StatusCreated)
	if ctx.responseErr == nil {
		t.Fatal("Status after render was accepted")
	}
}

func TestFreezeSafeResponseValidatesStatusHeadersAndBody(t *testing.T) {
	module := &Module{options: DefaultServerOptions()}
	newContext := func() *SafeContext {
		return &SafeContext{statusCode: http.StatusOK, header: make(http.Header)}
	}

	valid := newContext()
	valid.Header("X-Test", "value")
	valid.Data(http.StatusCreated, "application/octet-stream", []byte("body"))
	response, err := module.freezeSafeResponse(valid)
	if err != nil || response.StatusCode != http.StatusCreated || string(response.Body) != "body" {
		t.Fatalf("valid response=%+v error=%v", response, err)
	}
	response.Body[0] = 'x'
	response.Header.Set("X-Test", "changed")
	if string(valid.response) != "body" || valid.header.Get("X-Test") != "value" {
		t.Fatal("freeze exposed SafeContext storage")
	}

	invalidStatus := newContext()
	invalidStatus.statusCode = 99
	if _, err := module.freezeSafeResponse(invalidStatus); err == nil {
		t.Fatal("invalid status was accepted")
	}

	large := newContext()
	large.response = make([]byte, module.options.MaxSafeResponseBodySize+1)
	if _, err := module.freezeSafeResponse(large); err == nil {
		t.Fatal("large body was accepted")
	}

	for _, header := range []http.Header{
		{"Connection": {"close"}},
		{"Bad\nName": {"value"}},
		{"X-Test": {"bad\nvalue"}},
	} {
		ctx := newContext()
		ctx.header = header
		if _, err := module.freezeSafeResponse(ctx); err == nil {
			t.Fatalf("invalid header accepted: %v", header)
		}
	}

	limited := newContext()
	limited.header.Set("X-Large", "value")
	module.options.MaxHeaderBytes = 1
	if _, err := module.freezeSafeResponse(limited); err == nil {
		t.Fatal("oversized header was accepted")
	}
}

func TestInvalidSafeErrorMapperFallsBackToInternalResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	options := DefaultServerOptions()
	options.SafeErrorMapper = func(error) Response { return Response{StatusCode: 99} }
	module := &Module{options: options}
	module.commitMappedError(ginContext, context.DeadlineExceeded)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("fallback status = %d", recorder.Code)
	}
}
