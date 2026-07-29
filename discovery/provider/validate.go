package provider

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/duanhf2012/origin/v3/errs"
)

const (
	// MaxNodes 是一份完整 Provider 快照允许的 Node 数。
	MaxNodes = 8192
	// MaxServices 是一份完整 Provider 快照允许的公开 Service 总数。
	MaxServices = 65536
	// MaxServicesPerNode 是单 Node 允许的公开 Service 数。
	MaxServicesPerNode = 256
	// MaxLabelsPerNode 是单 Node 允许的 Label 数。
	MaxLabelsPerNode = 32
	// MaxRecordSize 是单个规范化 Node 的估算编码上限。
	MaxRecordSize = 256 * 1024
	// MaxSnapshotSize 是一份完整规范化快照的估算编码上限。
	MaxSnapshotSize = 16 * 1024 * 1024
)

// NormalizeSnapshot 校验、深复制并稳定排序完整快照。
//
// 返回值不再引用 Provider 的 Slice 或 Map，调用方可以在返回后立即复用输入容器。
func NormalizeSnapshot(snapshot Snapshot) (Snapshot, error) {
	if len(snapshot.Nodes) > MaxNodes {
		return Snapshot{}, capacityError("Node 数量超过上限")
	}
	result := Snapshot{Nodes: make([]Node, len(snapshot.Nodes))}
	seenNodes := make(map[string]struct{}, len(snapshot.Nodes))
	totalServices := 0
	totalSize := 0
	for index := range snapshot.Nodes {
		node, size, err := normalizeNode(snapshot.Nodes[index])
		if err != nil {
			return Snapshot{}, err
		}
		if _, duplicate := seenNodes[node.NodeID]; duplicate {
			return Snapshot{}, invalidSnapshot("NodeID 重复")
		}
		seenNodes[node.NodeID] = struct{}{}
		totalServices += len(node.Services)
		if totalServices > MaxServices {
			return Snapshot{}, capacityError("Service 总数超过上限")
		}
		totalSize += size
		if totalSize > MaxSnapshotSize {
			return Snapshot{}, capacityError("完整快照超过上限")
		}
		result.Nodes[index] = node
	}
	slices.SortFunc(result.Nodes, func(left, right Node) int {
		return strings.Compare(left.NodeID, right.NodeID)
	})
	return result, nil
}

// NormalizeNode 校验、深复制并稳定排序一条发布记录。
func NormalizeNode(input Node) (Node, error) {
	result, _, err := normalizeNode(input)
	return result, err
}

func normalizeNode(input Node) (Node, int, error) {
	if !validKebab(input.NodeID, 63) {
		return Node{}, 0, invalidSnapshot("NodeID 必须是 63 字节以内的小写 kebab-case")
	}
	if input.SessionID == 0 {
		return Node{}, 0, invalidSnapshot("SessionID 不能为零")
	}
	if len(input.Labels) > MaxLabelsPerNode {
		return Node{}, 0, capacityError("单 Node Label 数量超过上限")
	}
	if len(input.Services) == 0 || len(input.Services) > MaxServicesPerNode {
		return Node{}, 0, capacityError("单 Node Service 数量必须位于 1～256")
	}
	switch input.Transport {
	case TransportTCP:
		host, port, err := net.SplitHostPort(input.Address)
		portNumber, portErr := strconv.Atoi(port)
		if err != nil || portErr != nil ||
			strings.TrimSpace(host) == "" ||
			portNumber <= 0 || portNumber > 65535 ||
			!validText(input.Address, 255) {
			return Node{}, 0, invalidSnapshot("TCP Address 必须是可拨号 host:port")
		}
	case TransportNone, TransportNATS:
		if input.Address != "" {
			return Node{}, 0, invalidSnapshot("None/NATS Transport 不能携带 Address")
		}
	default:
		return Node{}, 0, invalidSnapshot("Transport 无效")
	}

	result := input
	result.Labels = make(map[string]string, len(input.Labels))
	size := len(input.NodeID) + len(input.Address) + 32
	for key, value := range input.Labels {
		if !validText(key, 63) || !validText(value, 255) {
			return Node{}, 0, invalidSnapshot("Label Key/Value 无效")
		}
		result.Labels[key] = value
		size += len(key) + len(value) + 4
	}
	result.Services = append([]Service(nil), input.Services...)
	slices.SortFunc(result.Services, func(left, right Service) int {
		return strings.Compare(left.ServiceName, right.ServiceName)
	})
	for index, service := range result.Services {
		if !validText(service.ServiceName, 255) {
			return Node{}, 0, invalidSnapshot("ServiceName 无效")
		}
		if service.State != ServiceStateRunning &&
			service.State != ServiceStateRetired {
			return Node{}, 0, invalidSnapshot("ServiceState 无效")
		}
		emptyFingerprint := service.ContractFingerprint == [32]byte{}
		if (service.ContractID == 0) != emptyFingerprint {
			return Node{}, 0, invalidSnapshot("RPC ContractID 与 Fingerprint 必须同时存在")
		}
		if index > 0 &&
			result.Services[index-1].ServiceName == service.ServiceName {
			return Node{}, 0, invalidSnapshot("ServiceName 重复")
		}
		size += len(service.ServiceName) + 48
	}
	if size > MaxRecordSize {
		return Node{}, 0, capacityError("单 Node 记录超过上限")
	}
	return result, size, nil
}

func validKebab(value string, maxBytes int) bool {
	if len(value) == 0 || len(value) > maxBytes ||
		value[0] < 'a' || value[0] > 'z' ||
		value[len(value)-1] == '-' {
		return false
	}
	previousDash := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z':
			previousDash = false
		case character >= '0' && character <= '9':
			previousDash = false
		case character == '-' && !previousDash:
			previousDash = true
		default:
			return false
		}
	}
	return true
}

func validText(value string, maxBytes int) bool {
	if value == "" || len(value) > maxBytes || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == 0 || character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func invalidSnapshot(message string) error {
	return errs.NewMessage(errs.CodeDiscoverySnapshotInvalid, message)
}

func capacityError(message string) error {
	return errs.NewMessage(errs.CodeDiscoveryCapacity, fmt.Sprintf("服务发现容量不足: %s", message))
}
