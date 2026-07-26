package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type benchmarkConfig struct {
	Servers []struct {
		ID      string
		Address string
	}
	Metadata map[string]string
	Payload  string
}

func BenchmarkLoadDir(b *testing.B) {
	b.Run("single-yaml", func(b *testing.B) {
		dir := b.TempDir()
		content := "servers:\n  - id: node-1\n    address: 127.0.0.1:7001\nmetadata:\n  padding: " +
			strings.Repeat("x", 4<<10) + "\n"
		writeBenchmarkFile(b, dir, "config.yaml", content)

		b.ReportAllocs()
		for b.Loop() {
			var cfg benchmarkConfig
			if err := LoadDir(dir, &cfg); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("one-hundred-mixed-files", func(b *testing.B) {
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

		b.ReportAllocs()
		for b.Loop() {
			var cfg benchmarkConfig
			if err := LoadDir(dir, &cfg); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("one-mebibyte-yaml", func(b *testing.B) {
		dir := b.TempDir()
		writeBenchmarkFile(b, dir, "config.yaml", "payload: "+strings.Repeat("x", 1<<20)+"\n")

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
	dir := b.TempDir()
	writeBenchmarkFile(b, dir, "config.yaml", "servers: []\nunknown: true\n")

	b.ReportAllocs()
	for b.Loop() {
		var cfg benchmarkConfig
		if err := LoadDir(dir, &cfg); err == nil {
			b.Fatal("未知字段应返回错误")
		}
	}
}

func writeBenchmarkFile(b *testing.B, root, relative, content string) {
	b.Helper()
	path := filepath.Join(root, relative)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		b.Fatal(err)
	}
}
