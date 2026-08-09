package admin

import (
	"net/http"
	"testing"
)

func TestResponseJSONAndEmpty(t *testing.T) {
	response, err := JSON(http.StatusOK, map[string]int{"value": 7})
	if err != nil || response.Status() != http.StatusOK ||
		string(response.Body()) != "{\"value\":7}" {
		t.Fatalf("JSON() = %+v, %v", response, err)
	}
	if response := Empty(http.StatusAccepted); response.Status() != http.StatusAccepted || len(response.Body()) != 0 {
		t.Fatalf("Empty() = %+v", response)
	}
	if _, err := JSON(http.StatusBadRequest, struct{}{}); err == nil {
		t.Fatal("JSON accepted non-2xx status")
	}
}

func TestResponseDoesNotExposeMutableBodyOrHeaders(t *testing.T) {
	response, err := JSON(http.StatusCreated, map[string]string{"state": "ready"})
	if err != nil {
		t.Fatalf("JSON() error = %v", err)
	}
	body := response.Body()
	headers := response.Header()
	body[0] = '['
	headers.Set("Content-Type", "text/plain")
	if string(response.Body()) != "{\"state\":\"ready\"}" || response.Header().Get("Content-Type") != "application/json" {
		t.Fatal("Response exposed mutable encoded data")
	}
	if _, err := JSON(http.StatusOK, make(chan int)); err == nil {
		t.Fatal("JSON accepted an unencodable value")
	}
	if string(response.encodedBody()) != "{\"state\":\"ready\"}" ||
		response.encodedHeader().Get("Content-Type") != "application/json" {
		t.Fatal("Response internal encoded view did not preserve the JSON result")
	}
}
