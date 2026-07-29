package discovery

import "testing"

// FuzzDirectoryApplySnapshot 验证任意有界原始快照都不会 panic；非法输入必须保持原目录不变。
func FuzzDirectoryApplySnapshot(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{1, 1, 1, 1, 1, 1})
	f.Add([]byte{2, 0, 3, 255, 4, 1, 9, 2})
	f.Fuzz(func(t *testing.T, data []byte) {
		filter, err := CompileFilter(false, nil)
		if err != nil {
			t.Fatalf("CompileFilter() error = %v", err)
		}
		directory, err := NewDirectory("local-1", filter)
		if err != nil {
			t.Fatalf("NewDirectory() error = %v", err)
		}
		baseline := RawSnapshot{Nodes: []RawNode{
			rawNode("baseline-1", "session-baseline", "127.0.0.1:1", "BaseService", 1),
		}}
		if _, _, err := directory.ApplySnapshot(baseline); err != nil {
			t.Fatalf("baseline ApplySnapshot() error = %v", err)
		}

		beforeVersion := directory.Version()
		before, _ := directory.Find("baseline-1", "BaseService")
		candidate := fuzzRawSnapshot(data)
		_, _, applyErr := directory.ApplySnapshot(candidate)
		if applyErr != nil {
			after, exists := directory.Find("baseline-1", "BaseService")
			if !exists ||
				after != before ||
				directory.Version() != beforeVersion {
				t.Fatalf(
					"非法快照污染目录: version=%d/%d before=%p after=%p",
					beforeVersion,
					directory.Version(),
					before,
					after,
				)
			}
		}
	})
}

// fuzzRawSnapshot 把任意字节限制为至多八个 Node 和每 Node 四个 Service，避免 Fuzz 自身
// 因无界测试数据分配掩盖目录边界问题。
func fuzzRawSnapshot(data []byte) RawSnapshot {
	if len(data) == 0 {
		return RawSnapshot{}
	}
	cursor := 0
	next := func() byte {
		value := data[cursor%len(data)]
		cursor++
		return value
	}
	nodeCount := int(next() % 9)
	result := RawSnapshot{Nodes: make([]RawNode, 0, nodeCount)}
	for nodeIndex := 0; nodeIndex < nodeCount; nodeIndex++ {
		nodeID := fuzzIdentifier("node", nodeIndex, next())
		if next()%7 == 0 {
			nodeID = ""
		}
		sessionID := fuzzIdentifier("session", nodeIndex, next())
		if next()%7 == 0 {
			sessionID = ""
		}
		transport := Transport(next() % 4)
		address := ""
		if transport == TransportTCP || next()%3 == 0 {
			address = "127.0.0.1:1"
		}
		serviceCount := int(next() % 5)
		node := RawNode{
			NodeID:    nodeID,
			SessionID: sessionID,
			Transport: transport,
			Address:   address,
			Services:  make([]RawService, 0, serviceCount),
		}
		if next()%2 == 0 {
			node.Labels = map[string]string{"region": fuzzIdentifier("r", nodeIndex, next())}
		}
		for serviceIndex := 0; serviceIndex < serviceCount; serviceIndex++ {
			name := fuzzIdentifier("service", serviceIndex, next())
			if next()%7 == 0 {
				name = ""
			}
			state := ServiceState(next() % 4)
			contract := next()
			service := RawService{
				ServiceName: name,
				State:       state,
			}
			if contract%3 != 0 {
				service.ContractID = uint64(contract)
				service.ContractFingerprint[0] = contract
			}
			if contract%5 == 0 {
				service.ContractFingerprint = [32]byte{}
			}
			node.Services = append(node.Services, service)
		}
		result.Nodes = append(result.Nodes, node)
	}
	return result
}

// fuzzIdentifier 创建可比较但可能重复的短标识，覆盖重复实例和正常排序路径。
func fuzzIdentifier(prefix string, index int, value byte) string {
	return prefix + "-" + string([]byte{
		byte('a' + index%26),
		byte('a' + int(value)%26),
	})
}
