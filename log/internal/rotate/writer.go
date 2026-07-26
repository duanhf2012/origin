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
	// Path 是当前活动文件路径。
	Path string
	// MaxSizeBytes 大于零时启用大小滚动。
	MaxSizeBytes int64
	// ByDate 和 UTC 共同决定自然日滚动边界。
	ByDate bool
	UTC    bool
	// MaxAge、MaxFiles 和 Compress 由维护协程应用于归档。
	MaxAge   time.Duration
	MaxFiles int
	Compress bool
	// Now 仅用于测试注入；生产默认使用 time.Now。
	Now func() time.Time
}

// Writer 是只允许一个日志协程调用 Write 和 Sync 的滚动文件。
type Writer struct {
	// config 构造后只读；file、size、day 只由日志协程访问。
	config      Config
	file        *os.File
	size        int64
	day         string
	terminalErr error

	// maintain 是容量为一的合并信号，worker 等待唯一维护协程退出。
	maintain chan struct{}
	worker   sync.WaitGroup

	closeOnce sync.Once
	closeErr  error

	// errorMu 只保护维护协程产生并由 Close 读取的累计错误。
	errorMu  sync.Mutex
	asyncErr error
}

// New 创建活动文件并启动唯一归档维护协程。
func New(config Config) (*Writer, error) {
	// 在创建目录和文件前完成纯配置校验。
	if err := validate(config); err != nil {
		return nil, err
	}
	// 测试未注入时钟时固定使用系统当前时间。
	if config.Now == nil {
		config.Now = time.Now
	}
	// 活动文件父目录由 Writer 负责建立。
	if err := os.MkdirAll(filepath.Dir(config.Path), 0o755); err != nil {
		return nil, err
	}

	// 以追加方式打开现有活动文件，并读取真实已有大小。
	file, size, err := openActive(config.Path)
	if err != nil {
		return nil, err
	}

	// 初始化只由写协程访问的文件状态和自然日标记。
	writer := &Writer{
		config:   config,
		file:     file,
		size:     size,
		day:      writerDay(config, config.Now()),
		maintain: make(chan struct{}, 1),
	}
	// 启动唯一维护协程并登记 WaitGroup 所有权。
	writer.worker.Add(1)
	go writer.maintenanceLoop()
	// 启动后立即清理上次进程遗留的临时或过期归档。
	writer.signalMaintenance()
	return writer, nil
}

// Write 在必要时先滚动，然后把完整数据追加到当前活动文件。
func (writer *Writer) Write(data []byte) (int, error) {
	// file=nil 表示已经关闭或滚动恢复失败，优先返回不可恢复原因。
	if writer.file == nil {
		if writer.terminalErr != nil {
			return 0, writer.terminalErr
		}
		return 0, os.ErrClosed
	}
	// 单次写入只读取一次时钟，保证日期判断与归档命名一致。
	now := writer.config.Now()
	// 空活动文件尚未归属于任何有内容日期，首条日志决定 day。
	if writer.config.ByDate && writer.size == 0 {
		writer.day = writerDay(writer.config, now)
	}
	// 在写入前判断，使触发边界的完整日志进入新文件而不被拆分。
	if writer.shouldRotate(now, len(data)) {
		if err := writer.rotate(now); err != nil {
			return 0, err
		}
	}

	// 写入后按实际成功字节数更新大小，短写和错误不会高估文件。
	count, err := writer.file.Write(data)
	writer.size += int64(count)
	return count, err
}

// Sync 把活动文件缓冲刷新到操作系统。
func (writer *Writer) Sync() error {
	// 与 Write 相同，关闭或终止状态优先返回原始原因。
	if writer.file == nil {
		if writer.terminalErr != nil {
			return writer.terminalErr
		}
		return os.ErrClosed
	}
	// Runtime 保证串行调用，无需额外互斥。
	return writer.file.Sync()
}

// Close 关闭活动文件、停止维护协程并汇总异步错误。
func (writer *Writer) Close() error {
	// 资源释放只能执行一次，后续调用复用首次结果。
	writer.closeOnce.Do(func() {
		// 先关闭活动文件并清除引用，阻止后续 Write/Sync 使用旧句柄。
		var fileErr error
		if writer.file != nil {
			fileErr = writer.file.Close()
			writer.file = nil
		}
		// 关闭信号通道使维护协程处理完已收到信号后退出。
		close(writer.maintain)
		writer.worker.Wait()

		// 维护错误由独立协程写入，等待退出后在锁内读取最终值。
		writer.errorMu.Lock()
		asyncErr := writer.asyncErr
		writer.errorMu.Unlock()
		// 文件关闭和维护错误都需要向上返回。
		writer.closeErr = errors.Join(fileErr, asyncErr)
	})
	return writer.closeErr
}

// shouldRotate 判断下一次完整写入是否应先切换活动文件。
func (writer *Writer) shouldRotate(now time.Time, nextSize int) bool {
	// 日期变化只滚动已有内容的文件，避免空文件每天产生归档。
	if writer.config.ByDate && writer.day != writerDay(writer.config, now) && writer.size > 0 {
		return true
	}
	// 大小阈值为零表示关闭；当前空文件即使单条超限也先允许写入。
	return writer.config.MaxSizeBytes > 0 &&
		writer.size > 0 &&
		writer.size+int64(nextSize) > writer.config.MaxSizeBytes
}

// rotate 把当前活动文件改名为唯一归档，并重新建立活动文件。
func (writer *Writer) rotate(now time.Time) error {
	// Windows 不允许重命名打开文件，因此必须先关闭。
	if err := writer.file.Close(); err != nil {
		return err
	}
	writer.file = nil

	// 在目标时区生成不会覆盖已有归档的候选路径。
	archive, err := nextArchivePath(writer.config.Path, inLocation(writer.config, now))
	if err != nil {
		return err
	}
	// 重命名失败时尽力重新打开活动文件，使后续错误保持可诊断。
	if err := os.Rename(writer.config.Path, archive); err != nil {
		reopened, size, openErr := openActive(writer.config.Path)
		writer.file = reopened
		writer.size = size
		writer.terminalErr = errors.Join(err, openErr)
		return writer.terminalErr
	}

	// 归档成功后创建新的活动文件；失败进入 terminalErr 状态。
	file, size, err := openActive(writer.config.Path)
	if err != nil {
		writer.terminalErr = err
		return err
	}
	// 一次性提交新文件状态，再异步触发归档维护。
	writer.file = file
	writer.size = size
	writer.day = writerDay(writer.config, now)
	writer.signalMaintenance()
	return nil
}

// signalMaintenance 合并重复维护请求，避免滚动频繁时积压无界任务。
func (writer *Writer) signalMaintenance() {
	// 容量一通道已含信号时直接跳过，因为一次扫描会处理全部归档。
	select {
	case writer.maintain <- struct{}{}:
	default:
	}
}

// maintenanceLoop 串行执行归档压缩和清理，并累计所有失败。
func (writer *Writer) maintenanceLoop() {
	// 无论通道如何结束都必须释放 WaitGroup。
	defer writer.worker.Done()
	for range writer.maintain {
		// 单次维护失败不终止协程，后续滚动仍可重试。
		if err := Maintain(writer.config); err != nil {
			writer.errorMu.Lock()
			writer.asyncErr = errors.Join(writer.asyncErr, err)
			writer.errorMu.Unlock()
		}
	}
}

// PrepareExisting 在 Crash 文件安装前滚动超限活动文件并同步维护归档。
func PrepareExisting(config Config) error {
	// Crash 输出没有长期 Writer，先复用相同配置边界校验。
	if err := validate(config); err != nil {
		return err
	}
	// 固化时钟并创建父目录，确保后续 Stat/Rename 目标有效。
	if config.Now == nil {
		config.Now = time.Now
	}
	if err := os.MkdirAll(filepath.Dir(config.Path), 0o755); err != nil {
		return err
	}

	info, err := os.Stat(config.Path)
	// 只在已有文件达到大小阈值时归档；不存在属于正常首次启动。
	switch {
	case err == nil && config.MaxSizeBytes > 0 && info.Size() >= config.MaxSizeBytes:
		// 先选择唯一归档名，再执行原子重命名。
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
	// 无论是否发生滚动，都同步处理遗留压缩临时文件和保留策略。
	return Maintain(config)
}

// validate 校验内部 Writer 不能自行恢复的基础边界。
func validate(config Config) error {
	// 活动文件必须有明确路径。
	if config.Path == "" {
		return errors.New("empty log path")
	}
	if config.MaxSizeBytes < 0 || config.MaxAge < 0 || config.MaxFiles < 0 {
		// 零表示关闭对应限制，负数没有定义。
		return errors.New("negative rotation value")
	}
	return nil
}

// openActive 以追加模式打开活动文件并返回其当前真实大小。
func openActive(path string) (*os.File, int64, error) {
	// O_APPEND 保证进程内每次 Write 都追加到文件尾。
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, 0, err
	}
	// 读取已有大小，使重启后的大小滚动继续使用正确基线。
	info, err := file.Stat()
	if err != nil {
		// Stat 失败时当前函数仍拥有句柄，必须在返回前关闭。
		_ = file.Close()
		return nil, 0, err
	}
	return file, info.Size(), nil
}

// nextArchivePath 生成带毫秒时间和可选序号的唯一归档路径。
func nextArchivePath(active string, now time.Time) (string, error) {
	// 保留原扩展名，并把时间插入文件名主体之后。
	extension := filepath.Ext(active)
	stem := strings.TrimSuffix(active, extension)
	base := stem + "-" + now.Format("2006-01-02T15-04-05.000")

	// 从无序号候选开始，发现冲突后顺序增加正整数后缀。
	for sequence := 0; ; sequence++ {
		candidate := base
		if sequence > 0 {
			candidate += "-" + strconv.Itoa(sequence)
		}
		candidate += extension
		// 未压缩、已压缩和压缩临时文件任一存在都视为占用该名称。
		_, err := os.Stat(candidate)
		_, gzipErr := os.Stat(candidate + ".gz")
		_, temporaryErr := os.Stat(candidate + ".gz.tmp")
		if errors.Is(err, os.ErrNotExist) &&
			errors.Is(gzipErr, os.ErrNotExist) &&
			errors.Is(temporaryErr, os.ErrNotExist) {
			return candidate, nil
		}
		// 只有“不存在”可用于继续选择；权限等异常必须立即返回。
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

// writerDay 返回配置时区中的稳定自然日标识。
func writerDay(config Config, now time.Time) string {
	// 日期字符串仅用于相等比较，不参与时间运算。
	return inLocation(config, now).Format("2006-01-02")
}

// inLocation 把时间转换到配置指定的 UTC 或系统本地时区。
func inLocation(config Config, value time.Time) time.Time {
	// UTC 是显式选择，其余合法配置均为 Local。
	if config.UTC {
		return value.UTC()
	}
	return value.Local()
}

// 编译期确认 Writer 满足 Zap AddSync 所需的关闭写入边界。
var _ io.WriteCloser = (*Writer)(nil)
