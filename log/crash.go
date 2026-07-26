package log

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
	"github.com/duanhf2012/origin/v3/log/internal/rotate"
)

// CrashOutput 是最终进程入口显式持有的 Runtime Crash 输出注册。
type CrashOutput struct {
	// closeOnce 保证进程级注册只撤销一次。
	closeOnce sync.Once
	// closeErr 保存首次撤销结果，供所有重复调用返回同一错误。
	closeErr error
}

// InstallCrashOutput 安装由 fileConfig 派生的唯一进程级 Crash 输出。
func InstallCrashOutput(fileConfig FileConfig) (*CrashOutput, error) {
	// Crash 输出必须依附已经启用且合法的文件配置，避免产生意外路径。
	if !fileConfig.Enabled {
		return nil, invalidConfig("crash output requires enabled file output")
	}
	if err := fileConfig.validate(); err != nil {
		return nil, err
	}

	// Crash 文件使用独立路径和保留策略，不复用普通日志 Writer 或协程。
	config := rotate.Config{
		Path:         crashPath(fileConfig.Path),
		MaxSizeBytes: fileConfig.Rotation.MaxSizeMB * 1024 * 1024,
		ByDate:       false,
		UTC:          fileConfig.Rotation.Timezone == UTCTime,
		MaxAge:       time.Duration(fileConfig.Retention.MaxAgeDays) * 24 * time.Hour,
		MaxFiles:     fileConfig.Retention.MaxFiles,
		Compress:     fileConfig.Retention.Compress,
	}
	// 注册新 Crash 文件前先归档已有文件，防止本次启动覆盖上次崩溃信息。
	if err := rotate.PrepareExisting(config); err != nil {
		return nil, errs.Wrap(errs.CodeLogOutputFailed, err)
	}

	// 以追加方式打开活动 Crash 文件，权限与普通日志文件保持一致。
	file, err := os.OpenFile(config.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, errs.Wrap(errs.CodeLogOutputFailed, err)
	}
	// SetCrashOutput 会复制操作系统句柄，因此注册完成后应关闭本地 *os.File。
	setErr := debug.SetCrashOutput(file, debug.CrashOptions{})
	closeErr := file.Close()
	// 注册或本地句柄关闭任一失败都视为安装失败，并尽力撤销已成功注册的句柄。
	if err := errors.Join(setErr, closeErr); err != nil {
		if setErr == nil {
			_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		}
		return nil, errs.Wrap(errs.CodeLogOutputFailed, err)
	}
	// 返回值只代表本次注册的撤销责任，不长期持有文件对象。
	return &CrashOutput{}, nil
}

// Close 取消当前进程级 Crash 输出注册。重复调用安全。
func (output *CrashOutput) Close() error {
	// nil 输出表示安装从未发生，关闭保持幂等成功。
	if output == nil {
		return nil
	}
	// 进程级 debug 注册不能重复撤销，使用 Once 固化首次结果。
	output.closeOnce.Do(func() {
		if err := debug.SetCrashOutput(nil, debug.CrashOptions{}); err != nil {
			output.closeErr = errs.Wrap(errs.CodeLogOutputFailed, err)
		}
	})
	// 重复调用返回同一个结果，调用方可以安全地在多条清理路径中使用。
	return output.closeErr
}

// crashPath 从普通活动日志路径派生独立 Crash 文件路径。
func crashPath(active string) string {
	// 没有扩展名时显式补充 .crash.log，保持文件用途清晰。
	extension := filepath.Ext(active)
	if extension == "" {
		return active + ".crash.log"
	}
	// 有扩展名时在原扩展名前插入 .crash，例如 origin.log -> origin.crash.log。
	return strings.TrimSuffix(active, extension) + ".crash" + extension
}
