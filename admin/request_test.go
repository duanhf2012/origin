package admin

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/duanhf2012/origin/v3/errs"
)

func TestRequestCopiesInputAndDecodesOneStrictJSONValue(t *testing.T) {
	query := url.Values{"mode": {"safe"}}
	header := http.Header{"X-Test": {"one"}}
	body := []byte(`{"version":42}`)
	request := NewRequest("req-1", Principal{Subject: "operator"}, query, header, body)
	query.Set("mode", "changed")
	header.Set("X-Test", "changed")
	body[0] = '['

	var input struct {
		Version uint64 `json:"version"`
	}
	if err := request.DecodeJSON(&input); err != nil || input.Version != 42 {
		t.Fatalf("DecodeJSON() = %+v, %v", input, err)
	}
	for _, payload := range [][]byte{
		nil,
		[]byte(`{"unknown":1}`),
		[]byte(`{"version":1}{"version":2}`),
	} {
		invalid := NewRequest("req", Principal{}, nil, nil, payload)
		if invalid.DecodeJSON(&input) == nil {
			t.Fatalf("payload %q unexpectedly decoded", payload)
		}
	}
}

func TestRequestAccessorsKeepOwnedCopies(t *testing.T) {
	principal := Principal{
		Subject:    "operator",
		Roles:      []string{"admin"},
		Attributes: map[string]string{"team": "ops"},
	}
	request := NewRequest(
		"request-1",
		principal,
		url.Values{"tag": {"first", "second"}},
		http.Header{"X-Trace": {"one", "two"}},
		[]byte("body"),
	)
	principal.Roles[0] = "changed"
	principal.Attributes["team"] = "changed"

	if request.ID() != "request-1" || request.Principal().Roles[0] != "admin" ||
		request.Principal().Attributes["team"] != "ops" || request.Query().Get("tag") != "first" ||
		request.Header().Get("X-Trace") != "one" || string(request.Body()) != "body" {
		t.Fatalf("Request accessors did not retain their owned values: %+v", request)
	}
	returnedPrincipal := request.Principal()
	returnedQuery := request.Query()
	returnedHeader := request.Header()
	returnedBody := request.Body()
	returnedPrincipal.Roles[0] = "changed"
	returnedPrincipal.Attributes["team"] = "changed"
	returnedQuery.Set("tag", "changed")
	returnedHeader.Set("X-Trace", "changed")
	returnedBody[0] = 'B'
	if request.Principal().Roles[0] != "admin" || request.Principal().Attributes["team"] != "ops" ||
		request.Query().Get("tag") != "first" || request.Header().Get("X-Trace") != "one" ||
		string(request.Body()) != "body" {
		t.Fatal("Request exposed mutable owned values")
	}
	if err := request.DecodeJSON(nil); !errs.IsCode(err, errs.CodeInvalidArgument) {
		t.Fatalf("DecodeJSON(nil) = %v, want CodeInvalidArgument", err)
	}
}
