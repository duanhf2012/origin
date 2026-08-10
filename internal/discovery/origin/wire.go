package origin

import (
	"encoding/binary"
	"math"
	"slices"
	"strings"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
)

const (
	frameHello     byte = 0x01
	framePublish   byte = 0x02
	frameWithdraw  byte = 0x03
	frameHeartbeat byte = 0x04
	frameResync    byte = 0x05

	frameHelloAck     byte = 0x81
	frameFullSnapshot byte = 0x82
	frameUpsertNode   byte = 0x83
	frameDeleteNode   byte = 0x84
	framePublishAck   byte = 0x85
	frameWithdrawAck  byte = 0x86
	frameHeartbeatAck byte = 0x87
	frameError        byte = 0xff

	syncWarming byte = 1
	syncReady   byte = 2
)

var errProtocol = errs.ErrTransportProtocol

type wireReader struct {
	data   []byte
	offset int
}

func encodeHello(nodeID string, sessionID uint64) []byte {
	result := []byte{frameHello}
	result = appendString(result, nodeID)
	return binary.BigEndian.AppendUint64(result, sessionID)
}

func decodeHello(data []byte) (string, uint64, error) {
	reader := wireReader{data: data}
	nodeID, err := reader.string()
	if err != nil {
		return "", 0, err
	}
	sessionID, err := reader.u64()
	if err != nil || sessionID == 0 || !reader.done() {
		return "", 0, errProtocol
	}
	return nodeID, sessionID, nil
}

func encodeHelloAck(epoch, revision uint64, state byte) []byte {
	result := []byte{frameHelloAck}
	result = binary.BigEndian.AppendUint64(result, epoch)
	result = binary.BigEndian.AppendUint64(result, revision)
	return append(result, state)
}

func decodeHelloAck(data []byte) (uint64, uint64, byte, error) {
	reader := wireReader{data: data}
	epoch, err := reader.u64()
	if err != nil || epoch == 0 {
		return 0, 0, 0, errProtocol
	}
	revision, err := reader.u64()
	if err != nil {
		return 0, 0, 0, errProtocol
	}
	state, err := reader.u8()
	if err != nil || (state != syncWarming && state != syncReady) || !reader.done() {
		return 0, 0, 0, errProtocol
	}
	return epoch, revision, state, nil
}

func encodePublish(node publicprovider.Node) ([]byte, error) {
	result := []byte{framePublish}
	return appendNode(result, node)
}

func decodePublish(data []byte) (publicprovider.Node, error) {
	reader := wireReader{data: data}
	node, err := reader.node()
	if err != nil || !reader.done() {
		return publicprovider.Node{}, errProtocol
	}
	return node, nil
}

func encodeFull(epoch, revision uint64, nodes []publicprovider.Node) ([]byte, error) {
	if len(nodes) > publicprovider.MaxNodes {
		return nil, errs.ErrDiscoveryCapacity
	}
	result := []byte{frameFullSnapshot}
	result = binary.BigEndian.AppendUint64(result, epoch)
	result = binary.BigEndian.AppendUint64(result, revision)
	result = binary.BigEndian.AppendUint16(result, uint16(len(nodes)))
	var err error
	for _, node := range nodes {
		result, err = appendNode(result, node)
		if err != nil {
			return nil, err
		}
		if len(result) > publicprovider.MaxSnapshotSize {
			return nil, errs.ErrDiscoveryCapacity
		}
	}
	return result, nil
}

func decodeFull(data []byte) (uint64, uint64, []publicprovider.Node, error) {
	reader := wireReader{data: data}
	epoch, err := reader.u64()
	if err != nil || epoch == 0 {
		return 0, 0, nil, errProtocol
	}
	revision, err := reader.u64()
	if err != nil {
		return 0, 0, nil, errProtocol
	}
	count, err := reader.u16()
	if err != nil || int(count) > publicprovider.MaxNodes {
		return 0, 0, nil, errProtocol
	}
	nodes := make([]publicprovider.Node, int(count))
	totalServices := 0
	for index := range nodes {
		nodes[index], err = reader.node()
		if err != nil {
			return 0, 0, nil, err
		}
		totalServices += len(nodes[index].Services)
		if totalServices > publicprovider.MaxServices {
			return 0, 0, nil, errProtocol
		}
		if index > 0 && nodes[index-1].NodeID >= nodes[index].NodeID {
			return 0, 0, nil, errProtocol
		}
	}
	if !reader.done() {
		return 0, 0, nil, errProtocol
	}
	return epoch, revision, nodes, nil
}

func encodeUpsert(revision uint64, node publicprovider.Node) ([]byte, error) {
	result := []byte{frameUpsertNode}
	result = binary.BigEndian.AppendUint64(result, revision)
	return appendNode(result, node)
}

func decodeUpsert(data []byte) (uint64, publicprovider.Node, error) {
	reader := wireReader{data: data}
	revision, err := reader.u64()
	if err != nil {
		return 0, publicprovider.Node{}, errProtocol
	}
	node, err := reader.node()
	if err != nil || !reader.done() {
		return 0, publicprovider.Node{}, errProtocol
	}
	return revision, node, nil
}

func encodeDelete(revision uint64, nodeID string, sessionID uint64) []byte {
	result := []byte{frameDeleteNode}
	result = binary.BigEndian.AppendUint64(result, revision)
	result = appendString(result, nodeID)
	return binary.BigEndian.AppendUint64(result, sessionID)
}

func decodeDelete(data []byte) (uint64, string, uint64, error) {
	reader := wireReader{data: data}
	revision, err := reader.u64()
	if err != nil {
		return 0, "", 0, errProtocol
	}
	nodeID, err := reader.string()
	if err != nil {
		return 0, "", 0, errProtocol
	}
	sessionID, err := reader.u64()
	if err != nil || sessionID == 0 || !reader.done() {
		return 0, "", 0, errProtocol
	}
	return revision, nodeID, sessionID, nil
}

func encodeAck(frame byte, revision uint64) []byte {
	result := []byte{frame}
	return binary.BigEndian.AppendUint64(result, revision)
}

func decodeAck(data []byte) (uint64, error) {
	reader := wireReader{data: data}
	revision, err := reader.u64()
	if err != nil || !reader.done() {
		return 0, errProtocol
	}
	return revision, nil
}

func encodeEmpty(frame byte) []byte {
	return []byte{frame}
}

func encodeError(code errs.Code) []byte {
	result := []byte{frameError}
	return binary.BigEndian.AppendUint32(result, uint32(code))
}

func decodeError(data []byte) (errs.Code, error) {
	reader := wireReader{data: data}
	code, err := reader.u32()
	if err != nil || code == 0 || !reader.done() {
		return 0, errProtocol
	}
	return errs.Code(code), nil
}

func appendNode(target []byte, node publicprovider.Node) ([]byte, error) {
	normalized, err := publicprovider.NormalizeNode(node)
	if err != nil {
		return nil, err
	}
	target = appendString(target, normalized.NodeID)
	target = binary.BigEndian.AppendUint64(target, normalized.SessionID)
	target = append(target, byte(normalized.Transport))
	target = appendString(target, normalized.Address)

	keys := make([]string, 0, len(normalized.Labels))
	for key := range normalized.Labels {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	target = append(target, byte(len(keys)))
	for _, key := range keys {
		target = appendString(target, key)
		target = appendString(target, normalized.Labels[key])
	}
	target = binary.BigEndian.AppendUint16(target, uint16(len(normalized.Services)))
	for _, service := range normalized.Services {
		target = appendString(target, service.ServiceName)
		target = append(target, byte(service.State))
		target = binary.BigEndian.AppendUint64(target, service.ContractID)
		target = append(target, service.ContractFingerprint[:]...)
	}
	if len(target) > publicprovider.MaxSnapshotSize {
		return nil, errs.ErrDiscoveryCapacity
	}
	return target, nil
}

func appendString(target []byte, value string) []byte {
	if len(value) > math.MaxUint8 {
		panic("origin discovery wire: string exceeds validated u8 length")
	}
	target = append(target, byte(len(value)))
	return append(target, value...)
}

func (reader *wireReader) node() (publicprovider.Node, error) {
	nodeID, err := reader.string()
	if err != nil {
		return publicprovider.Node{}, err
	}
	sessionID, err := reader.u64()
	if err != nil {
		return publicprovider.Node{}, err
	}
	transport, err := reader.u8()
	if err != nil {
		return publicprovider.Node{}, err
	}
	address, err := reader.string()
	if err != nil {
		return publicprovider.Node{}, err
	}
	labelCount, err := reader.u8()
	if err != nil || int(labelCount) > publicprovider.MaxLabelsPerNode {
		return publicprovider.Node{}, errProtocol
	}
	labels := make(map[string]string, int(labelCount))
	previousKey := ""
	for range int(labelCount) {
		key, readErr := reader.string()
		if readErr != nil || (previousKey != "" && previousKey >= key) {
			return publicprovider.Node{}, errProtocol
		}
		value, readErr := reader.string()
		if readErr != nil {
			return publicprovider.Node{}, errProtocol
		}
		labels[key] = value
		previousKey = key
	}
	serviceCount, err := reader.u16()
	if err != nil || int(serviceCount) > publicprovider.MaxServicesPerNode {
		return publicprovider.Node{}, errProtocol
	}
	services := make([]publicprovider.Service, int(serviceCount))
	for index := range services {
		name, readErr := reader.string()
		if readErr != nil ||
			(index > 0 && services[index-1].ServiceName >= name) {
			return publicprovider.Node{}, errProtocol
		}
		state, readErr := reader.u8()
		if readErr != nil {
			return publicprovider.Node{}, errProtocol
		}
		contractID, readErr := reader.u64()
		if readErr != nil {
			return publicprovider.Node{}, errProtocol
		}
		fingerprint, readErr := reader.bytes(32)
		if readErr != nil {
			return publicprovider.Node{}, errProtocol
		}
		services[index] = publicprovider.Service{
			ServiceName: name,
			State:       publicprovider.ServiceState(state),
			ContractID:  contractID,
		}
		copy(services[index].ContractFingerprint[:], fingerprint)
	}
	node, err := publicprovider.NormalizeNode(publicprovider.Node{
		NodeID:    nodeID,
		SessionID: sessionID,
		Labels:    labels,
		Transport: publicprovider.Transport(transport),
		Address:   address,
		Services:  services,
	})
	if err != nil {
		return publicprovider.Node{}, errProtocol
	}
	return node, nil
}

func (reader *wireReader) u8() (byte, error) {
	data, err := reader.bytes(1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

func (reader *wireReader) u16() (uint16, error) {
	data, err := reader.bytes(2)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(data), nil
}

func (reader *wireReader) u32() (uint32, error) {
	data, err := reader.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(data), nil
}

func (reader *wireReader) u64() (uint64, error) {
	data, err := reader.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(data), nil
}

func (reader *wireReader) string() (string, error) {
	length, err := reader.u8()
	if err != nil {
		return "", err
	}
	data, err := reader.bytes(int(length))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (reader *wireReader) bytes(length int) ([]byte, error) {
	if length < 0 || reader.offset > len(reader.data)-length {
		return nil, errProtocol
	}
	result := reader.data[reader.offset : reader.offset+length]
	reader.offset += length
	return result, nil
}

func (reader *wireReader) done() bool {
	return reader.offset == len(reader.data)
}

func nodeEqual(left, right publicprovider.Node) bool {
	if left.NodeID != right.NodeID || left.SessionID != right.SessionID ||
		left.Transport != right.Transport || left.Address != right.Address ||
		len(left.Labels) != len(right.Labels) ||
		len(left.Services) != len(right.Services) {
		return false
	}
	for key, value := range left.Labels {
		if right.Labels[key] != value {
			return false
		}
	}
	return slices.Equal(left.Services, right.Services)
}

func stableNodes(records map[string]publicprovider.Node) []publicprovider.Node {
	result := make([]publicprovider.Node, 0, len(records))
	for _, node := range records {
		result = append(result, node)
	}
	slices.SortFunc(result, func(left, right publicprovider.Node) int {
		return strings.Compare(left.NodeID, right.NodeID)
	})
	return result
}
