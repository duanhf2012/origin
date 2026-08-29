package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// benchmarkConfig 覆盖 Benchmark 样本需要的 Sequence、Map 和大字符串字段。
type benchmarkConfig struct {
	Servers []struct {
		ID      string `json:"id"`
		Address string `json:"address"`
	} `json:"servers"`
	Metadata map[string]string `json:"metadata"`
	Payload  string            `json:"payload"`
}

func BenchmarkLoadDir(b *testing.B) {
	b.Run("single-yaml", func(b *testing.B) {
		// 构造约 4 KiB 的单 YAML 文件，代表常见小型启动配置。
		dir := b.TempDir()
		content := "servers:\n  - id: node-1\n    address: 127.0.0.1:7001\nmetadata:\n  padding: " +
			strings.Repeat("x", 4<<10) + "\n"
		writeBenchmarkFile(b, dir, "config.yaml", content)

		// 循环包含扫描、读取、解析、合并和强类型解码完整成本。
		b.ReportAllocs()
		for b.Loop() {
			var cfg benchmarkConfig
			if err := LoadDir(dir, &cfg); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("one-hundred-mixed-files", func(b *testing.B) {
		// 交错建立 100 个约 4 KiB JSON/YAML 文件，模拟高度拆分配置。
		dir := b.TempDir()
		padding := strings.Repeat("x", 4<<10)
		for index := range 100 {
			if index%2 == 0 {
				writeBenchmarkFile(
					b,
					dir,
					fmt.Sprintf("%03d.yaml", index),
					fmt.Sprintf(
						"servers:\n  - id: node-%d\n    address: 127.0.0.1:%d\nmetadata:\n  file_%03d: %s\n",
						index,
						7000+index,
						index,
						padding,
					),
				)
				continue
			}
			writeBenchmarkFile(
				b,
				dir,
				fmt.Sprintf("%03d.json", index),
				fmt.Sprintf(
					`{"servers":[{"id":"node-%d","address":"127.0.0.1:%d"}],"metadata":{"file_%03d":"%s"}}`,
					index,
					7000+index,
					index,
					padding,
				),
			)
		}

		// 每轮从目录重新加载，观测文件数量增长下的整体复杂度。
		b.ReportAllocs()
		for b.Loop() {
			var cfg benchmarkConfig
			if err := LoadDir(dir, &cfg); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("one-mebibyte-yaml", func(b *testing.B) {
		// 使用单个大 Scalar 建立 1 MiB YAML 基线，避免元素数量干扰。
		dir := b.TempDir()
		writeBenchmarkFile(b, dir, "config.yaml", "payload: "+strings.Repeat("x", 1<<20)+"\n")

		// 每轮解码到新结构体，包含完整字符串所有权成本。
		b.ReportAllocs()
		for b.Loop() {
			var cfg benchmarkConfig
			if err := LoadDir(dir, &cfg); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkLoadDirRejectsUnknownField(b *testing.B) {
	// 固定一个包含未知顶层字段的小配置。
	dir := b.TempDir()
	writeBenchmarkFile(b, dir, "config.yaml", "servers: []\nunknown: true\n")

	// 失败路径同样报告分配，并要求每轮稳定拒绝。
	b.ReportAllocs()
	for b.Loop() {
		var cfg benchmarkConfig
		if err := LoadDir(dir, &cfg); err == nil {
			b.Fatal("未知字段应返回错误")
		}
	}
}

func BenchmarkFrozenSnapshotReads(b *testing.B) {
	dir := b.TempDir()
	writeBenchmarkFile(b, dir, "business.yaml", `
shared:
  timeout: 9
services:
  PlayerService:
    timeout: 7
    nested:
      enabled: true
    labels:
      zone: east
`)
	snapshot, err := LoadSnapshot(dir)
	if err != nil {
		b.Fatal(err)
	}
	serviceView, err := snapshot.Root().Lookup("services.PlayerService")
	if err != nil {
		b.Fatal(err)
	}

	b.Run("root_path", func(b *testing.B) {
		var timeout int
		b.ReportAllocs()
		for b.Loop() {
			view, lookupErr := snapshot.Root().Lookup("shared.timeout")
			if lookupErr != nil {
				b.Fatal(lookupErr)
			}
			if decodeErr := view.Decode(&timeout); decodeErr != nil {
				b.Fatal(decodeErr)
			}
		}
	})

	b.Run("service_field", func(b *testing.B) {
		var enabled bool
		b.ReportAllocs()
		for b.Loop() {
			view, lookupErr := serviceView.Lookup("nested.enabled")
			if lookupErr != nil {
				b.Fatal(lookupErr)
			}
			if decodeErr := view.Decode(&enabled); decodeErr != nil {
				b.Fatal(decodeErr)
			}
		}
	})

	b.Run("full_service", func(b *testing.B) {
		target := struct {
			Timeout int `json:"timeout"`
			Nested  struct {
				Enabled bool `json:"enabled"`
			} `json:"nested"`
			Labels map[string]string `json:"labels"`
		}{}
		b.ReportAllocs()
		for b.Loop() {
			if decodeErr := serviceView.Decode(&target); decodeErr != nil {
				b.Fatal(decodeErr)
			}
		}
	})
}

func writeBenchmarkFile(b *testing.B, root, relative, content string) {
	b.Helper()
	// 基准样本直接位于临时根目录，不把文件创建计入计时循环。
	path := filepath.Join(root, relative)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		b.Fatal(err)
	}
}
