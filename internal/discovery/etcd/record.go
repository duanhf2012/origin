package etcd

import (
	"slices"
	"strings"

	publicprovider "github.com/duanhf2012/origin/v3/discovery/provider"
	"github.com/duanhf2012/origin/v3/errs"
	"google.golang.org/protobuf/encoding/protowire"
)

const recordSchemaV1 = 1

func encodeRecord(network string, input publicprovider.Node) ([]byte, error) {
	if !validToken(network) {
		return nil, invalidRecord("Network 非法")
	}
	node, err := publicprovider.NormalizeNode(input)
	if err != nil {
		return nil, err
	}
	result := make([]byte, 0, 256)
	result = appendVarintField(result, 1, recordSchemaV1)
	result = appendStringField(result, 2, node.NodeID)
	result = appendVarintField(result, 3, node.SessionID)
	result = appendStringField(result, 4, network)
	result = appendVarintField(result, 5, uint64(node.Transport))
	if node.Address != "" {
		result = appendStringField(result, 6, node.Address)
	}
	keys := make([]string, 0, len(node.Labels))
	for key := range node.Labels {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		label := appendStringField(nil, 1, key)
		label = appendStringField(label, 2, node.Labels[key])
		result = appendBytesField(result, 7, label)
	}
	for _, service := range node.Services {
		encoded := appendStringField(nil, 1, service.ServiceName)
		encoded = appendVarintField(encoded, 2, uint64(service.State))
		if service.ContractID != 0 {
			encoded = appendVarintField(encoded, 3, service.ContractID)
			encoded = appendBytesField(
				encoded,
				4,
				service.ContractFingerprint[:],
			)
		}
		result = appendBytesField(result, 8, encoded)
	}
	if len(result) > publicprovider.MaxRecordSize {
		return nil, errs.ErrDiscoveryCapacity
	}
	return result, nil
}

func decodeRecord(data []byte) (string, publicprovider.Node, error) {
	if len(data) == 0 {
		return "", publicprovider.Node{}, invalidRecord("记录不能为空")
	}
	if len(data) > publicprovider.MaxRecordSize {
		return "", publicprovider.Node{}, errs.ErrDiscoveryCapacity
	}
	var (
		schema    uint64
		node      publicprovider.Node
		network   string
		seen      uint16
		labels    = make(map[string]string)
		services  []publicprovider.Service
		lastLabel string
		lastSvc   string
	)
	for len(data) > 0 {
		number, fieldType, consumed := protowire.ConsumeTag(data)
		if consumed < 0 {
			return "", publicprovider.Node{}, invalidRecord("Protobuf Tag 非法")
		}
		data = data[consumed:]
		switch number {
		case 1, 3, 5:
			if fieldType != protowire.VarintType ||
				seen&(1<<number) != 0 {
				return "", publicprovider.Node{}, invalidRecord("标量字段重复或类型非法")
			}
			seen |= 1 << number
			value, size := protowire.ConsumeVarint(data)
			if size < 0 {
				return "", publicprovider.Node{}, invalidRecord("Varint 非法")
			}
			data = data[size:]
			switch number {
			case 1:
				schema = value
			case 3:
				node.SessionID = value
			case 5:
				node.Transport = publicprovider.Transport(value)
			}
		case 2, 4, 6:
			if fieldType != protowire.BytesType ||
				seen&(1<<number) != 0 {
				return "", publicprovider.Node{}, invalidRecord("字符串字段重复或类型非法")
			}
			seen |= 1 << number
			value, size := protowire.ConsumeBytes(data)
			if size < 0 {
				return "", publicprovider.Node{}, invalidRecord("字符串字段非法")
			}
			data = data[size:]
			switch number {
			case 2:
				node.NodeID = string(value)
			case 4:
				network = string(value)
			case 6:
				node.Address = string(value)
			}
		case 7:
			if fieldType != protowire.BytesType {
				return "", publicprovider.Node{}, invalidRecord("Label 字段类型非法")
			}
			value, size := protowire.ConsumeBytes(data)
			if size < 0 {
				return "", publicprovider.Node{}, invalidRecord("Label 字段非法")
			}
			data = data[size:]
			key, labelValue, err := decodeLabel(value)
			if err != nil || (lastLabel != "" && lastLabel >= key) {
				return "", publicprovider.Node{}, invalidRecord("Label 未严格排序或重复")
			}
			labels[key] = labelValue
			lastLabel = key
		case 8:
			if fieldType != protowire.BytesType {
				return "", publicprovider.Node{}, invalidRecord("Service 字段类型非法")
			}
			value, size := protowire.ConsumeBytes(data)
			if size < 0 {
				return "", publicprovider.Node{}, invalidRecord("Service 字段非法")
			}
			data = data[size:]
			service, err := decodeService(value)
			if err != nil || (lastSvc != "" && lastSvc >= service.ServiceName) {
				return "", publicprovider.Node{}, invalidRecord("Service 未严格排序或重复")
			}
			services = append(services, service)
			lastSvc = service.ServiceName
		default:
			size := protowire.ConsumeFieldValue(number, fieldType, data)
			if size < 0 {
				return "", publicprovider.Node{}, invalidRecord("未知字段编码非法")
			}
			data = data[size:]
		}
	}
	if schema != recordSchemaV1 || !validToken(network) {
		return "", publicprovider.Node{}, invalidRecord("Schema 或 Network 非法")
	}
	node.Labels = labels
	node.Services = services
	normalized, err := publicprovider.NormalizeNode(node)
	if err != nil {
		if errs.IsCode(err, errs.CodeDiscoveryCapacity) {
			return "", publicprovider.Node{}, err
		}
		return "", publicprovider.Node{}, invalidRecord("Node DTO 非法")
	}
	return network, normalized, nil
}

func decodeLabel(data []byte) (string, string, error) {
	var key, value string
	var seen uint8
	for len(data) > 0 {
		number, fieldType, consumed := protowire.ConsumeTag(data)
		if consumed < 0 {
			return "", "", invalidRecord("Label Tag 非法")
		}
		data = data[consumed:]
		if number != 1 && number != 2 {
			size := protowire.ConsumeFieldValue(number, fieldType, data)
			if size < 0 {
				return "", "", invalidRecord("Label 未知字段非法")
			}
			data = data[size:]
			continue
		}
		if fieldType != protowire.BytesType || seen&(1<<number) != 0 {
			return "", "", invalidRecord("Label 字段重复或类型非法")
		}
		seen |= 1 << number
		raw, size := protowire.ConsumeBytes(data)
		if size < 0 {
			return "", "", invalidRecord("Label 字符串非法")
		}
		data = data[size:]
		if number == 1 {
			key = string(raw)
		} else {
			value = string(raw)
		}
	}
	if key == "" || value == "" {
		return "", "", invalidRecord("Label Key/Value 不能为空")
	}
	return key, value, nil
}

func decodeService(data []byte) (publicprovider.Service, error) {
	var result publicprovider.Service
	var seen uint8
	for len(data) > 0 {
		number, fieldType, consumed := protowire.ConsumeTag(data)
		if consumed < 0 {
			return publicprovider.Service{}, invalidRecord("Service Tag 非法")
		}
		data = data[consumed:]
		if number < 1 || number > 4 {
			size := protowire.ConsumeFieldValue(number, fieldType, data)
			if size < 0 {
				return publicprovider.Service{}, invalidRecord("Service 未知字段非法")
			}
			data = data[size:]
			continue
		}
		if seen&(1<<number) != 0 {
			return publicprovider.Service{}, invalidRecord("Service 字段重复")
		}
		seen |= 1 << number
		switch number {
		case 1, 4:
			if fieldType != protowire.BytesType {
				return publicprovider.Service{}, invalidRecord("Service Bytes 类型非法")
			}
			raw, size := protowire.ConsumeBytes(data)
			if size < 0 {
				return publicprovider.Service{}, invalidRecord("Service Bytes 非法")
			}
			data = data[size:]
			if number == 1 {
				result.ServiceName = string(raw)
			} else {
				if len(raw) != len(result.ContractFingerprint) {
					return publicprovider.Service{}, invalidRecord("Fingerprint 长度非法")
				}
				copy(result.ContractFingerprint[:], raw)
			}
		case 2, 3:
			if fieldType != protowire.VarintType {
				return publicprovider.Service{}, invalidRecord("Service Varint 类型非法")
			}
			value, size := protowire.ConsumeVarint(data)
			if size < 0 {
				return publicprovider.Service{}, invalidRecord("Service Varint 非法")
			}
			data = data[size:]
			if number == 2 {
				result.State = publicprovider.ServiceState(value)
			} else {
				result.ContractID = value
			}
		}
	}
	if result.ServiceName == "" {
		return publicprovider.Service{}, invalidRecord("ServiceName 不能为空")
	}
	return result, nil
}

func appendVarintField(target []byte, number protowire.Number, value uint64) []byte {
	target = protowire.AppendTag(target, number, protowire.VarintType)
	return protowire.AppendVarint(target, value)
}

func appendStringField(target []byte, number protowire.Number, value string) []byte {
	return appendBytesField(target, number, []byte(value))
}

func appendBytesField(target []byte, number protowire.Number, value []byte) []byte {
	target = protowire.AppendTag(target, number, protowire.BytesType)
	target = protowire.AppendVarint(target, uint64(len(value)))
	return append(target, value...)
}

func invalidRecord(message string) error {
	return errs.NewMessage(
		errs.CodeDiscoverySnapshotInvalid,
		"etcd 服务发现记录非法: "+strings.TrimSpace(message),
	)
}
