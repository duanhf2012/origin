package rotate

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriterRotatesBySize(t *testing.T) {
	t.Parallel()

	// 使用 5 B 阈值，使第二条完整写入触发一次滚动。
	path := filepath.Join(t.TempDir(), "origin.log")
	writer, err := New(Config{
		Path:         path,
		MaxSizeBytes: 5,
		MaxFiles:     10,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	// 首条 4 B 留在旧活动文件，第二条 2 B 应写入新文件。
	if _, err := writer.Write([]byte("1234")); err != nil {
		t.Fatalf("first Write() = %v", err)
	}
	if _, err := writer.Write([]byte("56")); err != nil {
		t.Fatalf("second Write() = %v", err)
	}
	if err := writer.Sync(); err != nil {
		t.Fatalf("Sync() = %v", err)
	}
	// 连续关闭两次验证 Writer 资源释放幂等。
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}
	if _, err := writer.Write([]byte("closed")); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Write() after Close = %v, want os.ErrClosed", err)
	}
	if err := writer.Sync(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("Sync() after Close = %v, want os.ErrClosed", err)
	}

	// 活动文件应只含触发滚动的第二条内容。
	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(active) = %v", err)
	}
	if string(active) != "56" {
		t.Fatalf("active content = %q, want 56", active)
	}
	// 唯一归档应保存第一条内容。
	archives := archiveNames(t, path)
	if len(archives) != 1 {
		t.Fatalf("archive count = %d, want 1: %v", len(archives), archives)
	}
	content, err := os.ReadFile(archives[0])
	if err != nil {
		t.Fatalf("ReadFile(archive) = %v", err)
	}
	if string(content) != "1234" {
		t.Fatalf("archive content = %q, want 1234", content)
	}
}

// TestWriterUsesExistingFileSizeAfterRestart 防止重启后把已有活动文件当成空文件，导致大小
// 滚动实际超过配置阈值才触发。
func TestWriterUsesExistingFileSizeAfterRestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "origin.log")
	if err := os.WriteFile(path, []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer, err := New(Config{Path: path, MaxSizeBytes: 5, MaxFiles: 10})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("56")); err != nil {
		t.Fatalf("Write() = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(active) != "56" {
		t.Fatalf("active content = %q, want 56", active)
	}
	archives := archiveNames(t, path)
	if len(archives) != 1 {
		t.Fatalf("archive count = %d, want 1", len(archives))
	}
	content, err := os.ReadFile(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "1234" {
		t.Fatalf("archive content = %q, want 1234", content)
	}
}

func TestWriterRotatesByDate(t *testing.T) {
	t.Parallel()

	// 原子时钟从 UTC 午夜前开始，可在写入之间安全推进。
	var unixNano atomic.Int64
	first := time.Date(2026, 7, 25, 23, 59, 0, 0, time.UTC)
	unixNano.Store(first.UnixNano())
	now := func() time.Time {
		return time.Unix(0, unixNano.Load()).UTC()
	}

	// 配置 UTC 日期滚动并注入可控时钟。
	path := filepath.Join(t.TempDir(), "origin.log")
	writer, err := New(Config{
		Path:     path,
		ByDate:   true,
		UTC:      true,
		MaxFiles: 10,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	// 第一天写一条，再把时钟推进到次日写第二条。
	if _, err := writer.Write([]byte("day-one")); err != nil {
		t.Fatalf("first Write() = %v", err)
	}
	unixNano.Store(first.Add(2 * time.Minute).UnixNano())
	if _, err := writer.Write([]byte("day-two")); err != nil {
		t.Fatalf("second Write() = %v", err)
	}
	// 关闭后验证次日内容在活动文件，前一日形成一个归档。
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(active) = %v", err)
	}
	if string(active) != "day-two" {
		t.Fatalf("active content = %q", active)
	}
	if got := len(archiveNames(t, path)); got != 1 {
		t.Fatalf("archive count = %d, want 1", got)
	}
}

func TestSizeAndDateRotateOnlyOnce(t *testing.T) {
	t.Parallel()

	// 同时让第二次写入跨日且超过大小阈值。
	var unixNano atomic.Int64
	first := time.Date(2026, 7, 25, 23, 59, 0, 0, time.UTC)
	unixNano.Store(first.UnixNano())
	now := func() time.Time {
		return time.Unix(0, unixNano.Load()).UTC()
	}

	// Writer 应把两个触发条件合并为一次 rotate 调用。
	path := filepath.Join(t.TempDir(), "origin.log")
	writer, err := New(Config{
		Path:         path,
		MaxSizeBytes: 5,
		ByDate:       true,
		UTC:          true,
		MaxFiles:     10,
		Now:          now,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if _, err := writer.Write([]byte("1234")); err != nil {
		t.Fatal(err)
	}
	unixNano.Store(first.Add(2 * time.Minute).UnixNano())
	if _, err := writer.Write([]byte("56")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	// 若重复滚动会产生两个归档，本断言锁定只有一个。
	if got := len(archiveNames(t, path)); got != 1 {
		t.Fatalf("archive count = %d, want 1", got)
	}
}

func TestWriterDayUsesConfiguredTimezone(t *testing.T) {
	t.Parallel()

	// 默认配置按本地时区返回输入自然日。
	localValue := time.Date(2026, 7, 25, 23, 30, 0, 0, time.Local)
	if got := writerDay(Config{}, localValue); got != "2026-07-25" {
		t.Fatalf("Local day = %q", got)
	}

	// UTC-2 的 23:30 转为 UTC 已是次日，验证显式 UTC 分支。
	offset := time.FixedZone("UTC-2", -2*60*60)
	value := time.Date(2026, 7, 25, 23, 30, 0, 0, offset)
	if got := writerDay(Config{UTC: true}, value); got != "2026-07-26" {
		t.Fatalf("UTC day = %q, want 2026-07-26", got)
	}
}

func TestMaintainCompressesAndLimitsArchives(t *testing.T) {
	t.Parallel()

	// 建立活动文件，维护过程不得修改它。
	directory := t.TempDir()
	active := filepath.Join(directory, "origin.log")
	if err := os.WriteFile(active, []byte("active"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 建立三个命名合法且修改时间不同的普通归档。
	now := time.Now()
	for index := range 3 {
		path := filepath.Join(directory, "origin-2026-07-25T00-00-0"+string(rune('0'+index))+".000.log")
		if err := os.WriteFile(path, []byte("archive-"+string(rune('0'+index))), 0o644); err != nil {
			t.Fatal(err)
		}
		modTime := now.Add(time.Duration(index-3) * time.Hour)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatal(err)
		}
	}
	// 再加入一个遗留临时文件和一个名称不合法的人工备份。
	if err := os.WriteFile(
		filepath.Join(directory, "origin-2026-07-24T00-00-00.000.log.gz.tmp"),
		[]byte("partial"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(directory, "origin-manual-backup.log")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 启用压缩并只保留最新两个合法归档。
	if err := Maintain(Config{
		Path:     active,
		MaxFiles: 2,
		Compress: true,
		Now:      func() time.Time { return now },
	}); err != nil {
		t.Fatalf("Maintain() = %v", err)
	}

	// 结果必须恰有两个可读取 gzip。
	archives := archiveNames(t, active)
	if len(archives) != 2 {
		t.Fatalf("archive count = %d, want 2: %v", len(archives), archives)
	}
	for _, archive := range archives {
		if !strings.HasSuffix(archive, ".gz") {
			t.Fatalf("archive is not compressed: %s", archive)
		}
		assertGzipReadable(t, archive)
	}
	// 活动文件和人工备份必须保持原样。
	if content, err := os.ReadFile(active); err != nil || string(content) != "active" {
		t.Fatalf("active file changed: %q, %v", content, err)
	}
	if content, err := os.ReadFile(unrelated); err != nil || string(content) != "keep" {
		t.Fatalf("unrelated file changed: %q, %v", content, err)
	}
}

// TestMaintainRecoversDuplicateCompressedArchive 固定压缩已完成、但进程尚未来得及删除普通
// 归档时的恢复语义：下一次维护应使用仍在的源归档替换 .gz，并完成源文件清理。
func TestMaintainRecoversDuplicateCompressedArchive(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	active := filepath.Join(directory, "origin.log")
	archive := filepath.Join(directory, "origin-2026-07-25T00-00-00.000.log")
	compressed := archive + ".gz"
	if err := os.WriteFile(archive, []byte("authoritative archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 模拟上一次进程在最终 .gz 已出现、普通源归档尚未删除时退出。无效内容可以确认
	// 本次维护确实替换了旧目标，而不是把重复文件静默保留。
	if err := os.WriteFile(compressed, []byte("stale gzip"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Maintain(Config{Path: active, Compress: true}); err != nil {
		t.Fatalf("Maintain() = %v", err)
	}
	if _, err := os.Stat(archive); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source archive still exists: %v", err)
	}
	assertGzipContent(t, compressed, "authoritative archive")
}

func TestPrepareExistingRotatesCrashFile(t *testing.T) {
	t.Parallel()

	// 建立大于 1 B 阈值的旧 Crash 活动文件。
	path := filepath.Join(t.TempDir(), "origin.crash.log")
	if err := os.WriteFile(path, []byte("old crash"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 安装前准备应把它滚动为归档。
	if err := PrepareExisting(Config{
		Path:         path,
		MaxSizeBytes: 1,
		MaxFiles:     10,
	}); err != nil {
		t.Fatalf("PrepareExisting() = %v", err)
	}
	// 旧活动路径应不存在，并出现一个归档。
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("active crash file still exists: %v", err)
	}
	if got := len(archiveNames(t, path)); got != 1 {
		t.Fatalf("archive count = %d, want 1", got)
	}
}

func TestMaintainRemovesExpiredArchive(t *testing.T) {
	t.Parallel()

	// 建立一个命名合法的旧归档，并把修改时间设为 48 小时前。
	directory := t.TempDir()
	active := filepath.Join(directory, "origin.log")
	archive := filepath.Join(directory, "origin-2026-07-01T00-00-00.000.log")
	if err := os.WriteFile(archive, []byte("expired"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(archive, old, old); err != nil {
		t.Fatal(err)
	}
	// 使用 24 小时期限执行维护。
	if err := Maintain(Config{
		Path:   active,
		MaxAge: 24 * time.Hour,
		Now:    func() time.Time { return now },
	}); err != nil {
		t.Fatalf("Maintain() = %v", err)
	}
	// 归档应被删除。
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("expired archive still exists: %v", err)
	}
}

func archiveNames(t *testing.T, active string) []string {
	t.Helper()
	// 复用生产扫描规则取得属于活动文件的归档。
	archives, err := scanArchives(active)
	if err != nil {
		t.Fatalf("scanArchives() = %v", err)
	}
	// 测试断言只关注完成归档，排除压缩临时文件。
	names := make([]string, 0, len(archives))
	for _, archive := range archives {
		if !strings.HasSuffix(archive.path, ".tmp") {
			names = append(names, archive.path)
		}
	}
	return names
}

func assertGzipReadable(t *testing.T, path string) {
	t.Helper()

	// 打开 gzip 文件并确保测试结束时释放源句柄。
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	// 建立 gzip Reader 并读取到 EOF，验证尾部校验信息完整。
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
}

func assertGzipContent(t *testing.T, path, want string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read gzip = %v, close = %v", err, closeErr)
	}
	if string(content) != want {
		t.Fatalf("gzip content = %q, want %q", content, want)
	}
}

func BenchmarkWriter(b *testing.B) {
	// 创建不滚动 Writer，测量活动文件顺序追加基线。
	path := filepath.Join(b.TempDir(), "origin.log")
	writer, err := New(Config{Path: path})
	if err != nil {
		b.Fatal(err)
	}
	defer writer.Close()

	// 使用固定小日志行并报告每次 Write 分配。
	data := []byte("2026-07-25 INFO benchmark message\n")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := writer.Write(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWriterRotate(b *testing.B) {
	// 设置 1 KiB 阈值和两个保留文件，持续触发滚动与维护。
	path := filepath.Join(b.TempDir(), "origin.log")
	writer, err := New(Config{
		Path:         path,
		MaxSizeBytes: 1024,
		MaxFiles:     2,
	})
	if err != nil {
		b.Fatal(err)
	}
	defer writer.Close()

	// 每四次左右写入触发一次大小滚动。
	data := []byte(strings.Repeat("x", 256))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := writer.Write(data); err != nil {
			b.Fatal(err)
		}
	}
}
