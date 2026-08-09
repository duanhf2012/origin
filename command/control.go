package command

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

// ControlAction 是运行中 Application 支持的进程级状态动作。
type ControlAction uint8

const (
	ControlActionRetire ControlAction = iota + 1
	ControlActionResume
)

const (
	controlActionRetireText = "retire"
	controlActionResumeText = "resume"
	maxControlRecordSize    = 4 * 1024
)

// ControlRequest 由 start 的控制邮箱投递，并由 Start Handler 完成一次。
type ControlRequest interface {
	Action() ControlAction
	Context() context.Context
	Complete(error)
}

type controlRequestRecord struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Deadline string `json:"deadline"`
}

type controlResponseRecord struct {
	ID        string    `json:"id"`
	Success   bool      `json:"success"`
	ErrorCode errs.Code `json:"error_code"`
	Message   string    `json:"message"`
}

func encodeControlRequest(record controlRequestRecord) ([]byte, error) {
	if err := validateControlRequest(record); err != nil {
		return nil, err
	}
	return encodeControlRecord(record)
}

func decodeControlRequest(data []byte) (controlRequestRecord, error) {
	var record controlRequestRecord
	if err := decodeControlRecord(data, &record); err != nil {
		return controlRequestRecord{}, err
	}
	if err := validateControlRequest(record); err != nil {
		return controlRequestRecord{}, err
	}
	return record, nil
}

func validateControlRequest(record controlRequestRecord) error {
	if err := validateControlID(record.ID); err != nil {
		return err
	}
	if _, err := parseControlAction(record.Action); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339Nano, record.Deadline); err != nil {
		return fmt.Errorf("deadline is not RFC3339Nano: %w", err)
	}
	return nil
}

func encodeControlResponse(record controlResponseRecord) ([]byte, error) {
	if err := validateControlResponse(record, record.ID); err != nil {
		return nil, err
	}
	return encodeControlRecord(record)
}

func decodeControlResponse(data []byte, requestID string) (controlResponseRecord, error) {
	var record controlResponseRecord
	if err := decodeControlRecord(data, &record); err != nil {
		return controlResponseRecord{}, err
	}
	if err := validateControlResponse(record, requestID); err != nil {
		return controlResponseRecord{}, err
	}
	return record, nil
}

func validateControlResponse(record controlResponseRecord, requestID string) error {
	if err := validateControlID(record.ID); err != nil {
		return err
	}
	if record.ID != requestID {
		return fmt.Errorf("response request ID %q does not match %q", record.ID, requestID)
	}
	if record.Success {
		if record.ErrorCode != errs.CodeOK || record.Message != "" {
			return fmt.Errorf("successful response must not contain an error")
		}
		return nil
	}
	if record.ErrorCode == errs.CodeOK || !isKnownControlErrorCode(record.ErrorCode) {
		return fmt.Errorf("invalid response error code %d", record.ErrorCode)
	}
	return nil
}

func encodeControlRecord(record any) ([]byte, error) {
	data, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if len(data) > maxControlRecordSize {
		return nil, fmt.Errorf("control record exceeds %d bytes", maxControlRecordSize)
	}
	return data, nil
}

func decodeControlRecord(data []byte, target any) error {
	if len(data) == 0 {
		return fmt.Errorf("control record is empty")
	}
	if len(data) > maxControlRecordSize {
		return fmt.Errorf("control record exceeds %d bytes", maxControlRecordSize)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateControlID(id string) error {
	if len(id) != 32 || id != strings.ToLower(id) {
		return fmt.Errorf("control request ID must be 32 lowercase hexadecimal characters")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return fmt.Errorf("control request ID is invalid: %w", err)
	}
	return nil
}

func parseControlAction(action string) (ControlAction, error) {
	switch action {
	case controlActionRetireText:
		return ControlActionRetire, nil
	case controlActionResumeText:
		return ControlActionResume, nil
	default:
		return 0, fmt.Errorf("unknown control action %q", action)
	}
}

func formatControlAction(action ControlAction) (string, error) {
	switch action {
	case ControlActionRetire:
		return controlActionRetireText, nil
	case ControlActionResume:
		return controlActionResumeText, nil
	default:
		return "", fmt.Errorf("unknown control action %d", action)
	}
}

func isKnownControlErrorCode(code errs.Code) bool {
	switch {
	case code <= errs.CodeConfigNotFound:
		return true
	case code >= errs.CodeServiceRetired && code <= errs.CodeServiceFailed:
		return true
	case code >= errs.CodeRPCNoRoute && code <= errs.CodeRPCBroadcastFailed:
		return true
	case code >= errs.CodeTransportUnavailable && code <= errs.CodeTransportMessageTooLarge:
		return true
	case code >= errs.CodeDiscoveryUnavailable && code <= errs.CodeDiscoverySnapshotInvalid:
		return true
	case code >= errs.CodeLogClosed && code <= errs.CodeLogOutputUnavailable:
		return true
	case code >= errs.CodeDiagnosticsUnavailable && code <= errs.CodeDiagnosticsStateConflict:
		return true
	default:
		return false
	}
}

func readBoundedRegularFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return nil, statErr
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("control path %q is not a regular file", path)
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxControlRecordSize+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	if len(data) > maxControlRecordSize {
		return nil, fmt.Errorf("control record %q exceeds %d bytes", path, maxControlRecordSize)
	}
	return data, nil
}

func writeControlRecordAtomic(path string, data []byte) (result error) {
	if len(data) == 0 {
		return fmt.Errorf("control record is empty")
	}
	if len(data) > maxControlRecordSize {
		return fmt.Errorf("control record exceeds %d bytes", maxControlRecordSize)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if result != nil {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
