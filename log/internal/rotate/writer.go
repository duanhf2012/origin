// Package rotate 实现日志包内部使用的活动文件滚动和归档维护。
package rotate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Config 是内部滚动 Writer 配置。
type Config struct {
	Path         string
	MaxSizeBytes int64
	ByDate       bool
	UTC          bool
	MaxAge       time.Duration
	MaxFiles     int
	Compress     bool
	Now          func() time.Time
}

// Writer 是只允许一个日志协程调用 Write 和 Sync 的滚动文件。
type Writer struct {
	config      Config
	file        *os.File
	size        int64
	day         string
	terminalErr error

	maintain chan struct{}
	worker   sync.WaitGroup

	closeOnce sync.Once
	closeErr  error

	errorMu  sync.Mutex
	asyncErr error
}

// New 创建活动文件并启动唯一归档维护协程。
func New(config Config) (*Writer, error) {
	if err := validate(config); err != nil {
		return nil, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(config.Path), 0o755); err != nil {
		return nil, err
	}

	file, size, err := openActive(config.Path)
	if err != nil {
		return nil, err
	}

	writer := &Writer{
		config:   config,
		file:     file,
		size:     size,
		day:      writerDay(config, config.Now()),
		maintain: make(chan struct{}, 1),
	}
	writer.worker.Add(1)
	go writer.maintenanceLoop()
	writer.signalMaintenance()
	return writer, nil
}

func (writer *Writer) Write(data []byte) (int, error) {
	if writer.file == nil {
		if writer.terminalErr != nil {
			return 0, writer.terminalErr
		}
		return 0, os.ErrClosed
	}
	now := writer.config.Now()
	if writer.config.ByDate && writer.size == 0 {
		writer.day = writerDay(writer.config, now)
	}
	if writer.shouldRotate(now, len(data)) {
		if err := writer.rotate(now); err != nil {
			return 0, err
		}
	}

	count, err := writer.file.Write(data)
	writer.size += int64(count)
	return count, err
}

func (writer *Writer) Sync() error {
	if writer.file == nil {
		if writer.terminalErr != nil {
			return writer.terminalErr
		}
		return os.ErrClosed
	}
	return writer.file.Sync()
}

func (writer *Writer) Close() error {
	writer.closeOnce.Do(func() {
		var fileErr error
		if writer.file != nil {
			fileErr = writer.file.Close()
			writer.file = nil
		}
		close(writer.maintain)
		writer.worker.Wait()

		writer.errorMu.Lock()
		asyncErr := writer.asyncErr
		writer.errorMu.Unlock()
		writer.closeErr = errors.Join(fileErr, asyncErr)
	})
	return writer.closeErr
}

func (writer *Writer) shouldRotate(now time.Time, nextSize int) bool {
	if writer.config.ByDate && writer.day != writerDay(writer.config, now) && writer.size > 0 {
		return true
	}
	return writer.config.MaxSizeBytes > 0 &&
		writer.size > 0 &&
		writer.size+int64(nextSize) > writer.config.MaxSizeBytes
}

func (writer *Writer) rotate(now time.Time) error {
	if err := writer.file.Close(); err != nil {
		return err
	}
	writer.file = nil

	archive, err := nextArchivePath(writer.config.Path, inLocation(writer.config, now))
	if err != nil {
		return err
	}
	if err := os.Rename(writer.config.Path, archive); err != nil {
		reopened, size, openErr := openActive(writer.config.Path)
		writer.file = reopened
		writer.size = size
		writer.terminalErr = errors.Join(err, openErr)
		return writer.terminalErr
	}

	file, size, err := openActive(writer.config.Path)
	if err != nil {
		writer.terminalErr = err
		return err
	}
	writer.file = file
	writer.size = size
	writer.day = writerDay(writer.config, now)
	writer.signalMaintenance()
	return nil
}

func (writer *Writer) signalMaintenance() {
	select {
	case writer.maintain <- struct{}{}:
	default:
	}
}

func (writer *Writer) maintenanceLoop() {
	defer writer.worker.Done()
	for range writer.maintain {
		if err := Maintain(writer.config); err != nil {
			writer.errorMu.Lock()
			writer.asyncErr = errors.Join(writer.asyncErr, err)
			writer.errorMu.Unlock()
		}
	}
}

// PrepareExisting 在 Crash 文件安装前滚动超限活动文件并同步维护归档。
func PrepareExisting(config Config) error {
	if err := validate(config); err != nil {
		return err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(config.Path), 0o755); err != nil {
		return err
	}

	info, err := os.Stat(config.Path)
	switch {
	case err == nil && config.MaxSizeBytes > 0 && info.Size() >= config.MaxSizeBytes:
		archive, archiveErr := nextArchivePath(config.Path, inLocation(config, config.Now()))
		if archiveErr != nil {
			return archiveErr
		}
		if err := os.Rename(config.Path, archive); err != nil {
			return err
		}
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	return Maintain(config)
}

func validate(config Config) error {
	if config.Path == "" {
		return errors.New("empty log path")
	}
	if config.MaxSizeBytes < 0 || config.MaxAge < 0 || config.MaxFiles < 0 {
		return errors.New("negative rotation value")
	}
	return nil
}

func openActive(path string) (*os.File, int64, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

func nextArchivePath(active string, now time.Time) (string, error) {
	extension := filepath.Ext(active)
	stem := strings.TrimSuffix(active, extension)
	base := stem + "-" + now.Format("2006-01-02T15-04-05.000")

	for sequence := 0; ; sequence++ {
		candidate := base
		if sequence > 0 {
			candidate += "-" + strconv.Itoa(sequence)
		}
		candidate += extension
		_, err := os.Stat(candidate)
		_, gzipErr := os.Stat(candidate + ".gz")
		_, temporaryErr := os.Stat(candidate + ".gz.tmp")
		if errors.Is(err, os.ErrNotExist) &&
			errors.Is(gzipErr, os.ErrNotExist) &&
			errors.Is(temporaryErr, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect archive path: %w", err)
		}
		if gzipErr != nil && !errors.Is(gzipErr, os.ErrNotExist) {
			return "", fmt.Errorf("inspect compressed archive path: %w", gzipErr)
		}
		if temporaryErr != nil && !errors.Is(temporaryErr, os.ErrNotExist) {
			return "", fmt.Errorf("inspect temporary archive path: %w", temporaryErr)
		}
	}
}

func writerDay(config Config, now time.Time) string {
	return inLocation(config, now).Format("2006-01-02")
}

func inLocation(config Config, value time.Time) time.Time {
	if config.UTC {
		return value.UTC()
	}
	return value.Local()
}

var _ io.WriteCloser = (*Writer)(nil)
