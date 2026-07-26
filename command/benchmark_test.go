package command

import (
	"io"
	"runtime"
	"testing"
)

func BenchmarkValidateKebabName(b *testing.B) {
	// AppName 校验属于低频启动路径，基准只用于防止后续引入明显分配或复杂正则。
	b.ReportAllocs()
	var err error
	for b.Loop() {
		err = validateKebabName("game-server-1", "app name")
	}
	runtime.KeepAlive(err)
}

func BenchmarkParseNodeIDs(b *testing.B) {
	// 使用常见四 Node 启动参数记录切分、去空白、去重所需的基础成本。
	b.ReportAllocs()
	option := &stringOption{
		value: "gateway-1, game-1, chat-1, db-1",
		set:   true,
	}
	var nodeIDs []string
	var err error
	for b.Loop() {
		nodeIDs, err = parseNodeIDs(option)
	}
	runtime.KeepAlive(nodeIDs)
	runtime.KeepAlive(err)
}

func BenchmarkWriteGeneralHelp(b *testing.B) {
	// 帮助生成不在热路径，只验证自定义命令排序没有出现数量级退化。
	runner, err := New(Options{
		ProgramName: "game-server",
		Stdout:      io.Discard,
		Start:       noOpStart,
	})
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	for _, name := range []string{"check-config", "export-data", "repair-index"} {
		if err := runner.Register(Command{
			Name:    name,
			Summary: "benchmark command",
			Usage:   "game-server " + name,
			Run:     func(Context, []string) error { return nil },
		}); err != nil {
			b.Fatalf("Register(%q) error = %v", name, err)
		}
	}

	b.ReportAllocs()
	for b.Loop() {
		if err := runner.writeGeneralHelp(); err != nil {
			b.Fatalf("writeGeneralHelp() error = %v", err)
		}
	}
}
