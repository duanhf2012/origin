package command

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxKebabNameLength 与已确认的 AppName 和自定义命令名称上限保持一致。
const maxKebabNameLength = 63

// stringOption 记录一个字符串 flag 是否由使用者显式提供。
//
// `--node` 未出现和显式传入空字符串具有不同语义，因此不能只依赖最终字符串值。
type stringOption struct {
	// value 保存默认值或最近一次显式设置值。
	value string
	// set 区分选项完全未出现和显式传入空字符串。
	set bool
	// rejectDuplicate 用于只允许声明一次的单值选项。
	rejectDuplicate bool
}

// String 返回 flag 包显示当前值所需的文本。
func (option *stringOption) String() string {
	return option.value
}

// Set 保存显式参数值并标记该选项已经出现。
func (option *stringOption) Set(value string) error {
	if option.rejectDuplicate && option.set {
		return fmt.Errorf("option is duplicated")
	}
	option.value = value
	option.set = true
	return nil
}

// registerSingleStringOption 注册只能显式声明一次的字符串选项。
func registerSingleStringOption(
	flags *flag.FlagSet,
	name string,
	defaultValue string,
	usage string,
) *stringOption {
	option := &stringOption{value: defaultValue, rejectDuplicate: true}
	flags.Var(option, name, usage)
	return option
}

// hasDuplicateFlag 在交给 flag 包前识别重复单值选项，避免 flag.Value.Set 的错误文本
// 自动拼接第二个原始值。扫描只判断选项 token，不保存或回显地址内容。
func hasDuplicateFlag(args []string, name string) bool {
	longName := "--" + name
	shortName := "-" + name
	seen := false
	for _, arg := range args {
		if arg == "--" {
			break
		}
		matched := arg == longName || arg == shortName ||
			strings.HasPrefix(arg, longName+"=") || strings.HasPrefix(arg, shortName+"=")
		if !matched {
			continue
		}
		if seen {
			return true
		}
		seen = true
	}
	return false
}

// registerStringOption 把带默认值和显式出现状态的字符串选项注册到 FlagSet。
func registerStringOption(
	flags *flag.FlagSet,
	name string,
	defaultValue string,
	usage string,
) *stringOption {
	// 每次解析创建独立 option，避免不同 Runner 或多次 Run 共享状态。
	option := &stringOption{value: defaultValue}
	flags.Var(option, name, usage)
	return option
}

// validateKebabName 校验 AppName 和自定义命令共享的小写 kebab-case 规则。
func validateKebabName(name string, label string) error {
	// 长度先行校验可以把空值和超长值在扫描字符前快速拒绝。
	if len(name) == 0 {
		return invalidArgumentf("%s is required", label)
	}
	if len(name) > maxKebabNameLength {
		return invalidArgumentf("%s %q exceeds %d ASCII characters", label, name, maxKebabNameLength)
	}

	// 首字符只能是小写字母；随后允许小写字母、数字和不连续的中划线。
	if name[0] < 'a' || name[0] > 'z' {
		return invalidArgumentf("%s %q must start with a lowercase letter", label, name)
	}
	previousHyphen := false
	for index := 1; index < len(name); index++ {
		current := name[index]
		switch {
		case current >= 'a' && current <= 'z':
			previousHyphen = false
		case current >= '0' && current <= '9':
			previousHyphen = false
		case current == '-':
			if previousHyphen {
				return invalidArgumentf("%s %q contains consecutive hyphens", label, name)
			}
			previousHyphen = true
		default:
			return invalidArgumentf("%s %q must use lowercase kebab-case", label, name)
		}
	}
	if previousHyphen {
		return invalidArgumentf("%s %q must not end with a hyphen", label, name)
	}
	return nil
}

// parseNodeIDs 解析逗号分隔 NodeID，保持声明顺序并拒绝空项和重复项。
func parseNodeIDs(option *stringOption) ([]string, error) {
	// 完全未提供 --node 时返回非 nil 空切片，明确表达“由 Application 使用配置顺序”。
	if !option.set {
		return []string{}, nil
	}

	parts := strings.Split(option.value, ",")
	nodeIDs := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for index, part := range parts {
		// 只清理每一项两侧空白，不改变 NodeID 内部字符或大小写。
		nodeID := strings.TrimSpace(part)
		if nodeID == "" {
			return nil, invalidArgumentf("node list item %d is empty", index+1)
		}
		if _, exists := seen[nodeID]; exists {
			return nil, invalidArgumentf("node %q is duplicated", nodeID)
		}
		seen[nodeID] = struct{}{}
		nodeIDs = append(nodeIDs, nodeID)
	}
	return nodeIDs, nil
}

// resolveExistingDir 清理并绝对化必须已经存在的目录。
func resolveExistingDir(path string, label string) (string, error) {
	// 空白路径容易意外解析为当前目录，必须在 filepath.Abs 前显式拒绝。
	if strings.TrimSpace(path) == "" {
		return "", invalidConfigf("%s is empty", label)
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", invalidConfigf("resolve %s %q: %v", label, path, err)
	}

	// 配置目录只验证存在和目录类型；内容读取留给以后接入的 Start Handler。
	info, err := os.Stat(absolute)
	if err != nil {
		return "", invalidConfigf("inspect %s %q: %v", label, absolute, err)
	}
	if !info.IsDir() {
		return "", invalidConfigf("%s %q is not a directory", label, absolute)
	}
	return absolute, nil
}

// resolvePIDDir 清理、绝对化并创建 PID 控制目录。
func resolvePIDDir(path string) (string, error) {
	// 与配置目录不同，PID 目录属于 command 的资源，可以在 start 时按确认权限创建。
	if strings.TrimSpace(path) == "" {
		return "", processControlf("pid directory is empty")
	}
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", processControlf("resolve pid directory %q: %v", path, err)
	}
	if err := os.MkdirAll(absolute, 0o750); err != nil {
		return "", processControlf("create pid directory %q: %v", absolute, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", processControlf("inspect pid directory %q: %v", absolute, err)
	}
	if !info.IsDir() {
		return "", processControlf("pid path %q is not a directory", absolute)
	}
	return absolute, nil
}

// resolvePIDDirForStop 清理 stop 使用的 PID 目录，但不创建不存在的目录。
func resolvePIDDirForStop(path string) (absolute string, exists bool, err error) {
	// stop 不应为了幂等检查创建控制目录，因此与 start 使用独立解析入口。
	if strings.TrimSpace(path) == "" {
		return "", false, processControlf("pid directory is empty")
	}
	absolute, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", false, processControlf("resolve pid directory %q: %v", path, err)
	}
	info, statErr := os.Stat(absolute)
	if os.IsNotExist(statErr) {
		return absolute, false, nil
	}
	if statErr != nil {
		return "", false, processControlf("inspect pid directory %q: %v", absolute, statErr)
	}
	if !info.IsDir() {
		return "", false, processControlf("pid path %q is not a directory", absolute)
	}
	return absolute, true, nil
}

// rejectPositionals 确保内置命令没有未识别的位置参数。
func rejectPositionals(flags *flag.FlagSet) error {
	if flags.NArg() == 0 {
		return nil
	}
	return invalidArgumentf(
		"command %q does not accept positional arguments: %s",
		flags.Name(),
		fmt.Sprint(flags.Args()),
	)
}
