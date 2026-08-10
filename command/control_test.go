package command

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

const testControlID = "0123456789abcdef0123456789abcdef"

func TestControlRequestCodecIsStrictAndBounded(t *testing.T) {
	t.Parallel()

	deadline := time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)
	valid := []byte(`{"id":"` + testControlID + `","action":"retire","deadline":"` + deadline + `"}`)
	request, err := decodeControlRequest(valid)
	if err != nil || request.Action != controlActionRetireText {
		t.Fatalf("decodeControlRequest() = (%+v, %v)", request, err)
	}
	for _, invalid := range [][]byte{
		append(valid[:len(valid)-1], []byte(`,"extra":true}`)...),
		[]byte(`{"id":"short","action":"retire","deadline":"` + deadline + `"}`),
		[]byte(`{"id":"` + testControlID + `","action":"stop","deadline":"` + deadline + `"}`),
		[]byte(`{"id":"` + testControlID + `","action":"retire","deadline":"not-a-time"}`),
		append(append([]byte(nil), valid...), []byte(` {}`)...),
		bytes.Repeat([]byte{'x'}, maxControlRecordSize+1),
	} {
		if _, err := decodeControlRequest(invalid); err == nil {
			t.Fatalf("decodeControlRequest(%q) error = nil", invalid)
		}
	}
}

func TestControlResponseCodecRejectsInvalidRecords(t *testing.T) {
	t.Parallel()

	valid, err := encodeControlResponse(controlResponseRecord{ID: testControlID, Success: true})
	if err != nil {
		t.Fatal(err)
	}
	response, err := decodeControlResponse(valid, testControlID)
	if err != nil || !response.Success {
		t.Fatalf("decodeControlResponse() = (%+v, %v)", response, err)
	}

	failure, err := encodeControlResponse(controlResponseRecord{
		ID:        testControlID,
		ErrorCode: errs.CodeDiscoveryUnavailable,
		Message:   "provider unavailable",
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err = decodeControlResponse(failure, testControlID)
	if err != nil || response.ErrorCode != errs.CodeDiscoveryUnavailable {
		t.Fatalf("failure response = (%+v, %v)", response, err)
	}

	for _, invalid := range [][]byte{
		[]byte(`{"id":"` + testControlID + `","success":true,"error_code":0,"message":"","extra":true}`),
		[]byte(`{"id":"` + testControlID + `","success":false,"error_code":9999,"message":"bad"}`),
		[]byte(`{"id":"` + testControlID + `","success":false,"error_code":0,"message":"bad"}`),
		[]byte(`{"id":"` + testControlID + `","success":true,"error_code":3,"message":"bad"}`),
	} {
		if _, err := decodeControlResponse(invalid, testControlID); err == nil {
			t.Fatalf("decodeControlResponse(%q) error = nil", invalid)
		}
	}
	if _, err := decodeControlResponse(valid, "abcdef0123456789abcdef0123456789"); err == nil {
		t.Fatal("decodeControlResponse() accepted mismatched request ID")
	}
}

func TestControlResponseCodecAcceptsCurrentAdminErrors(t *testing.T) {
	t.Parallel()

	for _, code := range []errs.Code{
		errs.CodeAdminUnavailable,
		errs.CodeAdminStateConflict,
	} {
		encoded, err := encodeControlResponse(controlResponseRecord{
			ID:        testControlID,
			ErrorCode: code,
			Message:   "admin control error",
		})
		if err != nil {
			t.Fatalf("encodeControlResponse(%d) error = %v", code, err)
		}
		response, err := decodeControlResponse(encoded, testControlID)
		if err != nil || response.ErrorCode != code {
			t.Fatalf(
				"decodeControlResponse(%d) = (%+v, %v)",
				code,
				response,
				err,
			)
		}
	}
}

func TestControlPathsUsePIDDirectoryAndApplicationPrefix(t *testing.T) {
	t.Parallel()

	pidDir := filepath.Join(t.TempDir(), "run")
	want := map[string]string{
		controlLockPath(pidDir, "game"):       "game.control.lock",
		controlRequestPath(pidDir, "game"):    "game.control.request",
		controlProcessingPath(pidDir, "game"): "game.control.processing",
		controlResponsePath(pidDir, "game"):   "game.control.response",
	}
	for path, base := range want {
		if filepath.Dir(path) != pidDir || filepath.Base(path) != base {
			t.Fatalf("control path = %q, want directory %q and base %q", path, pidDir, base)
		}
	}
}

func TestControlRecordIOIsBoundedAndRequiresRegularFile(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "game.control.request")
	data := []byte(`{"id":"` + testControlID + `"}`)
	if err := writeControlRecordAtomic(path, data); err != nil {
		t.Fatal(err)
	}
	got, err := readBoundedRegularFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("readBoundedRegularFile() = (%q, %v), want %q", got, err, data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("record permissions = %o, want private", info.Mode().Perm())
	}

	if err := writeControlRecordAtomic(path, bytes.Repeat([]byte{'x'}, maxControlRecordSize+1)); err == nil {
		t.Fatal("writeControlRecordAtomic() accepted oversized record")
	}
	directoryPath := filepath.Join(directory, "not-a-file")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedRegularFile(directoryPath); err == nil {
		t.Fatal("readBoundedRegularFile() accepted directory")
	}
}
