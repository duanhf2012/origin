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
	closeOnce sync.Once
	closeErr  error
}

// InstallCrashOutput 安装由 fileConfig 派生的唯一进程级 Crash 输出。
func InstallCrashOutput(fileConfig FileConfig) (*CrashOutput, error) {
	if !fileConfig.Enabled {
		return nil, invalidConfig("crash output requires enabled file output")
	}
	if err := fileConfig.validate(); err != nil {
		return nil, err
	}

	config := rotate.Config{
		Path:         crashPath(fileConfig.Path),
		MaxSizeBytes: fileConfig.Rotation.MaxSizeMB * 1024 * 1024,
		ByDate:       false,
		UTC:          fileConfig.Rotation.Timezone == UTCTime,
		MaxAge:       time.Duration(fileConfig.Retention.MaxAgeDays) * 24 * time.Hour,
		MaxFiles:     fileConfig.Retention.MaxFiles,
		Compress:     fileConfig.Retention.Compress,
	}
	if err := rotate.PrepareExisting(config); err != nil {
		return nil, errs.Wrap(errs.CodeLogOutputFailed, err)
	}

	file, err := os.OpenFile(config.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, errs.Wrap(errs.CodeLogOutputFailed, err)
	}
	setErr := debug.SetCrashOutput(file, debug.CrashOptions{})
	closeErr := file.Close()
	if err := errors.Join(setErr, closeErr); err != nil {
		if setErr == nil {
			_ = debug.SetCrashOutput(nil, debug.CrashOptions{})
		}
		return nil, errs.Wrap(errs.CodeLogOutputFailed, err)
	}
	return &CrashOutput{}, nil
}

// Close 取消当前进程级 Crash 输出注册。重复调用安全。
func (output *CrashOutput) Close() error {
	if output == nil {
		return nil
	}
	output.closeOnce.Do(func() {
		if err := debug.SetCrashOutput(nil, debug.CrashOptions{}); err != nil {
			output.closeErr = errs.Wrap(errs.CodeLogOutputFailed, err)
		}
	})
	return output.closeErr
}

func crashPath(active string) string {
	extension := filepath.Ext(active)
	if extension == "" {
		return active + ".crash.log"
	}
	return strings.TrimSuffix(active, extension) + ".crash" + extension
}
