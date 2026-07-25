package rotate

import (
	"compress/gzip"
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

	path := filepath.Join(t.TempDir(), "origin.log")
	writer, err := New(Config{
		Path:         path,
		MaxSizeBytes: 5,
		MaxFiles:     10,
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}
	if _, err := writer.Write([]byte("1234")); err != nil {
		t.Fatalf("first Write() = %v", err)
	}
	if _, err := writer.Write([]byte("56")); err != nil {
		t.Fatalf("second Write() = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("second Close() = %v", err)
	}

	active, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(active) = %v", err)
	}
	if string(active) != "56" {
		t.Fatalf("active content = %q, want 56", active)
	}
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

func TestWriterRotatesByDate(t *testing.T) {
	t.Parallel()

	var unixNano atomic.Int64
	first := time.Date(2026, 7, 25, 23, 59, 0, 0, time.UTC)
	unixNano.Store(first.UnixNano())
	now := func() time.Time {
		return time.Unix(0, unixNano.Load()).UTC()
	}

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
	if _, err := writer.Write([]byte("day-one")); err != nil {
		t.Fatalf("first Write() = %v", err)
	}
	unixNano.Store(first.Add(2 * time.Minute).UnixNano())
	if _, err := writer.Write([]byte("day-two")); err != nil {
		t.Fatalf("second Write() = %v", err)
	}
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

	var unixNano atomic.Int64
	first := time.Date(2026, 7, 25, 23, 59, 0, 0, time.UTC)
	unixNano.Store(first.UnixNano())
	now := func() time.Time {
		return time.Unix(0, unixNano.Load()).UTC()
	}

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
	if got := len(archiveNames(t, path)); got != 1 {
		t.Fatalf("archive count = %d, want 1", got)
	}
}

func TestWriterDayUsesConfiguredTimezone(t *testing.T) {
	t.Parallel()

	localValue := time.Date(2026, 7, 25, 23, 30, 0, 0, time.Local)
	if got := writerDay(Config{}, localValue); got != "2026-07-25" {
		t.Fatalf("Local day = %q", got)
	}

	offset := time.FixedZone("UTC-2", -2*60*60)
	value := time.Date(2026, 7, 25, 23, 30, 0, 0, offset)
	if got := writerDay(Config{UTC: true}, value); got != "2026-07-26" {
		t.Fatalf("UTC day = %q, want 2026-07-26", got)
	}
}

func TestMaintainCompressesAndLimitsArchives(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	active := filepath.Join(directory, "origin.log")
	if err := os.WriteFile(active, []byte("active"), 0o644); err != nil {
		t.Fatal(err)
	}

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

	if err := Maintain(Config{
		Path:     active,
		MaxFiles: 2,
		Compress: true,
		Now:      func() time.Time { return now },
	}); err != nil {
		t.Fatalf("Maintain() = %v", err)
	}

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
	if content, err := os.ReadFile(active); err != nil || string(content) != "active" {
		t.Fatalf("active file changed: %q, %v", content, err)
	}
	if content, err := os.ReadFile(unrelated); err != nil || string(content) != "keep" {
		t.Fatalf("unrelated file changed: %q, %v", content, err)
	}
}

func TestPrepareExistingRotatesCrashFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "origin.crash.log")
	if err := os.WriteFile(path, []byte("old crash"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := PrepareExisting(Config{
		Path:         path,
		MaxSizeBytes: 1,
		MaxFiles:     10,
	}); err != nil {
		t.Fatalf("PrepareExisting() = %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("active crash file still exists: %v", err)
	}
	if got := len(archiveNames(t, path)); got != 1 {
		t.Fatalf("archive count = %d, want 1", got)
	}
}

func TestMaintainRemovesExpiredArchive(t *testing.T) {
	t.Parallel()

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
	if err := Maintain(Config{
		Path:   active,
		MaxAge: 24 * time.Hour,
		Now:    func() time.Time { return now },
	}); err != nil {
		t.Fatalf("Maintain() = %v", err)
	}
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("expired archive still exists: %v", err)
	}
}

func archiveNames(t *testing.T, active string) []string {
	t.Helper()
	archives, err := scanArchives(active)
	if err != nil {
		t.Fatalf("scanArchives() = %v", err)
	}
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

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if _, err := io.ReadAll(reader); err != nil {
		t.Fatal(err)
	}
}

func BenchmarkWriter(b *testing.B) {
	path := filepath.Join(b.TempDir(), "origin.log")
	writer, err := New(Config{Path: path})
	if err != nil {
		b.Fatal(err)
	}
	defer writer.Close()

	data := []byte("2026-07-25 INFO benchmark message\n")
	b.ReportAllocs()
	for b.Loop() {
		if _, err := writer.Write(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWriterRotate(b *testing.B) {
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

	data := []byte(strings.Repeat("x", 256))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := writer.Write(data); err != nil {
			b.Fatal(err)
		}
	}
}
