package rpc

import (
	"context"
	"strings"
	"testing"
)

type generatedContractTestDispatcher struct{}

func (*generatedContractTestDispatcher) ContractID() ContractID { return 7 }
func (*generatedContractTestDispatcher) Fingerprint() ContractFingerprint {
	return ContractFingerprint{9}
}
func (*generatedContractTestDispatcher) Dispatch(
	context.Context,
	MethodID,
	CallKind,
	[]byte,
	ResponseWriter,
) (ResponseWriter, error) {
	return ResponseWriter{}, nil
}

func validGeneratedContractDescriptor() GeneratedContractDescriptor {
	return GeneratedContractDescriptor{
		ServiceName:  "GeneratedRegistryPlayerService",
		ContractName: "example.com/playerapi.PlayerService",
		ContractID:   7,
		Fingerprint:  ContractFingerprint{9},
		NewDispatcher: func(any) (Dispatcher, bool) {
			return &generatedContractTestDispatcher{}, true
		},
	}
}

func TestGeneratedContractRegistryFindsDescriptor(t *testing.T) {
	var registry generatedContractRegistry
	descriptor := validGeneratedContractDescriptor()
	registry.register(descriptor)

	found, ok, err := registry.find(descriptor.ServiceName)
	if err != nil || !ok {
		t.Fatalf("find() descriptor=%+v ok=%v error=%v", found, ok, err)
	}
	if found.ContractName != descriptor.ContractName ||
		found.ContractID != descriptor.ContractID ||
		found.Fingerprint != descriptor.Fingerprint {
		t.Fatalf("find() descriptor=%+v", found)
	}
	if _, ok, err := registry.find("MissingService"); err != nil || ok {
		t.Fatalf("find(missing) ok=%v error=%v", ok, err)
	}
}

func TestGeneratedContractRegistryRejectsInvalidAndConflictingDescriptors(t *testing.T) {
	t.Run("invalid", func(t *testing.T) {
		var registry generatedContractRegistry
		registry.register(GeneratedContractDescriptor{})
		if _, _, err := registry.find("anything"); err == nil ||
			!strings.Contains(err.Error(), "ServiceName") {
			t.Fatalf("find() error=%v", err)
		}
	})

	t.Run("conflict", func(t *testing.T) {
		var registry generatedContractRegistry
		first := validGeneratedContractDescriptor()
		registry.register(first)
		second := first
		second.ContractName = "example.com/other.PlayerService"
		second.ContractID++
		registry.register(second)
		if _, _, err := registry.find(first.ServiceName); err == nil ||
			!strings.Contains(err.Error(), "同时关联契约") {
			t.Fatalf("find() error=%v", err)
		}
	})

	t.Run("idempotent", func(t *testing.T) {
		var registry generatedContractRegistry
		descriptor := validGeneratedContractDescriptor()
		registry.register(descriptor)
		registry.register(descriptor)
		if _, ok, err := registry.find(descriptor.ServiceName); err != nil || !ok {
			t.Fatalf("find() ok=%v error=%v", ok, err)
		}
	})
}
