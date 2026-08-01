package config

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/duanhf2012/origin/v3/errs"
)

// Snapshot 是一次目录加载、环境变量展开和跨文件合并后的不可变配置树。
//
// Snapshot 自身及其派生 View 可被多个 goroutine 并发读取。内部节点不会通过公开
// API 暴露，因而调用方无法修改已经交给 Application 的运行时配置。
type Snapshot struct {
	root *valueNode
}

// View 是 Snapshot 中一个节点的廉价只读视图。
//
// View 的零值表示配置缺失；对零值执行 Decode 会验证目标后保持目标不变。
type View struct {
	node *valueNode
}

// LoadSnapshot 递归加载目录中的 JSON/YAML 文件并冻结合并后的配置树。
func LoadSnapshot(directory string) (*Snapshot, error) {
	files, err := scanDir(directory)
	if err != nil {
		return nil, err
	}
	var root *valueNode
	for _, file := range files {
		current, err := parseFile(file)
		if err != nil {
			return nil, err
		}
		if err := expandEnvironment(current); err != nil {
			return nil, err
		}
		if root == nil {
			root = current
			continue
		}
		if err := mergeNodes(root, current, ""); err != nil {
			return nil, err
		}
	}
	return &Snapshot{root: root}, nil
}

// Root 返回完整配置根节点的只读视图。
func (snapshot *Snapshot) Root() View {
	if snapshot == nil {
		return View{}
	}
	return View{node: snapshot.root}
}

// Decode 按框架配置的严格语义解码完整 Snapshot。
func (snapshot *Snapshot) Decode(destination any) error {
	return snapshot.Root().decode(destination, true)
}

// Valid 报告当前 View 是否指向一个实际存在的配置节点。
func (view View) Valid() bool {
	return view.node != nil
}

// Lookup 按点分隔的 Mapping 路径派生子视图。
//
// 空路径返回当前 View；路径不存在、穿过非 Mapping 节点或包含空段时统一返回
// ErrConfigNotFound，调用方不需要依赖内部配置树类型。
func (view View) Lookup(path string) (View, error) {
	if path == "" && view.Valid() {
		return view, nil
	}
	current := view.node
	for _, segment := range strings.Split(path, ".") {
		if current == nil || current.kind != kindMapping || segment == "" {
			return View{}, configNotFound(path)
		}
		var next *valueNode
		for _, entry := range current.mapping {
			if entry.key == segment {
				next = entry.value
				break
			}
		}
		if next == nil {
			return View{}, configNotFound(path)
		}
		current = next
	}
	return View{node: current}, nil
}

// Decode 按业务配置的宽松语义解码当前 View。
//
// 未知字段被忽略，缺失字段保留 destination 的预填值或 Go 零值。
func (view View) Decode(destination any) error {
	return view.decode(destination, false)
}

// DecodeStrict 按框架配置语义解码当前 View，未知字段会返回错误。
func (view View) DecodeStrict(destination any) error {
	return view.decode(destination, true)
}

func (view View) decode(destination any, rejectUnknown bool) error {
	target, err := validateTarget(destination)
	if err != nil {
		return err
	}
	if view.node == nil {
		return nil
	}

	// 始终先写入临时值；解码器对指针、Map 和 Slice 使用写时复制，因此任意深度
	// 的类型错误都不会把半成品暴露给调用方。
	temporary := reflect.New(target.Type()).Elem()
	temporary.Set(target)
	decoder := valueDecoder{
		fields:        make(map[reflect.Type]structFields),
		rejectUnknown: rejectUnknown,
	}
	if err := decoder.decode(temporary, view.node); err != nil {
		return err
	}
	target.Set(temporary)
	return nil
}

func configNotFound(path string) error {
	return errs.NewMessage(
		errs.CodeConfigNotFound,
		fmt.Sprintf("配置路径 %q 不存在", path),
	)
}
