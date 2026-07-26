package config

// mergeNodes 把 source 按固定规则合入 destination，并保留双方来源。
func mergeNodes(destination, source *valueNode, path string) error {
	// 不同节点类型没有通用覆盖语义，直接报告首次和再次定义。
	if destination.kind != source.kind {
		return duplicateNodeError(destination, source, path)
	}

	// 只有 Mapping 和 Sequence 可以跨文件组合，Scalar/Null 重复均失败。
	switch destination.kind {
	case kindMapping:
		// 为目标 Mapping 建立临时 Key 索引，使本层合并保持线性复杂度。
		indexByKey := make(map[string]int, len(destination.mapping))
		for index, entry := range destination.mapping {
			indexByKey[entry.key] = index
		}
		for _, sourceEntry := range source.mapping {
			// 路径只用于错误说明，不影响实际 Key 匹配。
			childPath := joinPath(path, sourceEntry.key)
			index, exists := indexByKey[sourceEntry.key]
			if !exists {
				// 新 Key 保持 source 的稳定顺序追加，并登记新索引。
				indexByKey[sourceEntry.key] = len(destination.mapping)
				destination.mapping = append(destination.mapping, sourceEntry)
				continue
			}
			// 已有 Key 递归执行相同规则，允许父 Mapping 分散在多个文件。
			if err := mergeNodes(destination.mapping[index].value, sourceEntry.value, childPath); err != nil {
				return err
			}
		}
		return nil
	case kindSequence:
		// Sequence 没有按元素 ID 合并语义，只按文件顺序稳定追加。
		destination.sequence = append(destination.sequence, source.sequence...)
		return nil
	default:
		// Scalar、Null 以及任何无效节点都不能由后文件覆盖。
		return duplicateNodeError(destination, source, path)
	}
}

// duplicateNodeError 生成同时包含首次和再次定义来源的冲突错误。
func duplicateNodeError(first, second *valueNode, path string) error {
	// 根节点冲突没有自然字段名，使用明确占位。
	if path == "" {
		path = "<root>"
	}
	// 错误定位放在第二次定义处，并在正文中同时指出首次来源。
	return invalidConfigAt(
		second.source,
		"配置路径 %q 重复或类型冲突（首次定义 %s:%d:%d，原类型 %s，新类型 %s）",
		path,
		first.source.file,
		first.source.line,
		first.source.column,
		first.kindName(),
		second.kindName(),
	)
}
