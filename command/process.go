package command

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/duanhf2012/origin/v3/command/internal/processlock"
	"github.com/duanhf2012/origin/v3/errs"
)

// maxPIDRecordSize 是损坏 PID 诊断文件的读取保底，不是面向使用者的配置项。
const maxPIDRecordSize = 64 * 1024

// pidRecord 是 PID 文件中唯一允许保存的诊断信息。
type pidRecord struct {
	// PID 是当前持锁进程的操作系统进程号。
	PID int `json:"pid"`
	// StartedAt 是带操作系统本地数字时区偏移的 RFC 3339 启动时间。
	StartedAt string `json:"started_at"`
}

// pidLease 持有 start 进程的 PID 文件句柄和操作系统独占锁。
type pidLease struct {
	// file 是已经成功获得平台独占锁的固定 PID 文件句柄。
	file *os.File
	// path 用于清理错误定位，不参与锁身份判断。
	path string
	// closed 保证上层正常路径和 panic 保底路径可以重复清理。
	closed bool
}

// pidFilePath 根据已校验 AppName 生成固定 PID 文件路径。
func pidFilePath(pidDir string, appName string) string {
	return filepath.Join(pidDir, appName+".pid")
}

// stopFilePath 根据已校验 AppName 生成 Windows 普通控制台停止请求路径。
func stopFilePath(pidDir string, appName string) string {
	return filepath.Join(pidDir, appName+".stop")
}

// acquirePIDLease 打开固定 PID 文件并尝试立即取得进程运行权。
func acquirePIDLease(pidDir string, appName string) (*pidLease, error) {
	path := pidFilePath(pidDir, appName)

	// O_TRUNC 必须在获得锁后才能执行，否则第二个 start 会覆盖活动进程的诊断记录。
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, processControlf("open pid file for app %q at %q: %v", appName, path, err)
	}

	// 非阻塞加锁把普通竞争和系统错误分开；竞争进程只关闭自己的文件句柄。
	acquired, lockErr := processlock.TryLock(file)
	if lockErr != nil {
		closeErr := file.Close()
		return nil, processControlf(
			"lock pid file for app %q at %q: %v",
			appName,
			path,
			errors.Join(lockErr, closeErr),
		)
	}
	if !acquired {
		closeErr := file.Close()
		if closeErr != nil {
			return nil, processControlf(
				"close contended pid file for app %q at %q: %v",
				appName,
				path,
				closeErr,
			)
		}
		return nil, errs.NewMessage(
			errs.CodeProcessAlreadyRunning,
			fmt.Sprintf("application %q is already running; pid file %q is locked", appName, path),
		)
	}

	lease := &pidLease{file: file, path: path}
	if err := lease.writeRecord(); err != nil {
		// 写入失败后立即释放已经取得的运行权，不能留下半初始化的活动锁。
		releaseErr := lease.close()
		return nil, processControlf(
			"write pid record for app %q at %q: %v",
			appName,
			path,
			errors.Join(err, releaseErr),
		)
	}
	return lease, nil
}

// writeRecord 在同一个已锁定文件对象上覆盖 PID 诊断记录并同步到文件系统。
func (lease *pidLease) writeRecord() error {
	record := pidRecord{
		PID:       os.Getpid(),
		StartedAt: time.Now().In(time.Local).Format(time.RFC3339),
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// 按 Truncate、Seek、Write、Sync 固定顺序更新，绝不能用 rename 替换被锁文件。
	if err := lease.file.Truncate(0); err != nil {
		return err
	}
	if _, err := lease.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := lease.file.Write(data); err != nil {
		return err
	}
	return lease.file.Sync()
}

// close 幂等释放 PID 独占锁和文件句柄。
func (lease *pidLease) close() error {
	if lease == nil || lease.closed {
		return nil
	}
	// 先提交本地状态，避免错误回滚路径重复对已经关闭的句柄执行系统调用。
	lease.closed = true
	return processlock.Release(lease.file)
}

// readRunningPID 检查 PID 锁并在目标运行时严格读取其诊断记录。
func readRunningPID(path string) (running bool, pid int, err error) {
	// stop 不创建 PID 文件；文件不存在就是幂等的“未运行”。
	file, openErr := os.OpenFile(path, os.O_RDWR, 0o600)
	if os.IsNotExist(openErr) {
		return false, 0, nil
	}
	if openErr != nil {
		return false, 0, processControlf("open pid file %q: %v", path, openErr)
	}

	// 能获得锁说明没有活动 start 持有运行权，立即释放且绝不修改 PID 内容。
	acquired, lockErr := processlock.TryLock(file)
	if lockErr != nil {
		closeErr := file.Close()
		return false, 0, processControlf(
			"inspect pid lock %q: %v",
			path,
			errors.Join(lockErr, closeErr),
		)
	}
	if acquired {
		releaseErr := processlock.Release(file)
		if releaseErr != nil {
			return false, 0, processControlf("release idle pid lock %q: %v", path, releaseErr)
		}
		return false, 0, nil
	}

	// 锁由目标持有时只读取有限大小的诊断记录，防止损坏文件造成无界内存占用。
	data, readErr := io.ReadAll(io.LimitReader(file, maxPIDRecordSize+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil {
		return true, 0, processControlf(
			"read active pid file %q: %v",
			path,
			errors.Join(readErr, closeErr),
		)
	}
	if len(data) > maxPIDRecordSize {
		return true, 0, processControlf("active pid file %q exceeds %d bytes", path, maxPIDRecordSize)
	}

	record, decodeErr := decodePIDRecord(data)
	if decodeErr != nil {
		return true, 0, processControlf("decode active pid file %q: %v", path, decodeErr)
	}
	return true, record.PID, nil
}

// decodePIDRecord 严格校验 PID JSON、字段集合、PID 范围和本地时间文本。
func decodePIDRecord(data []byte) (pidRecord, error) {
	var record pidRecord
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	// 第一次 Decode 必须得到完整对象，第二次必须立即 EOF，拒绝拼接多个 JSON 值。
	if err := decoder.Decode(&record); err != nil {
		return pidRecord{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return pidRecord{}, fmt.Errorf("multiple JSON values")
		}
		return pidRecord{}, err
	}
	if record.PID <= 0 {
		return pidRecord{}, fmt.Errorf("pid must be positive")
	}
	if _, err := time.Parse(time.RFC3339, record.StartedAt); err != nil {
		return pidRecord{}, fmt.Errorf("started_at is not RFC3339: %w", err)
	}
	return record, nil
}

// isPIDLocked 只判断目标是否仍持有运行权，不读取或改写诊断内容。
func isPIDLocked(path string) (bool, error) {
	file, openErr := os.OpenFile(path, os.O_RDWR, 0o600)
	if os.IsNotExist(openErr) {
		return false, nil
	}
	if openErr != nil {
		return false, processControlf("open pid file %q while waiting: %v", path, openErr)
	}

	acquired, lockErr := processlock.TryLock(file)
	if lockErr != nil {
		closeErr := file.Close()
		return false, processControlf(
			"inspect pid lock %q while waiting: %v",
			path,
			errors.Join(lockErr, closeErr),
		)
	}
	if !acquired {
		if closeErr := file.Close(); closeErr != nil {
			return true, processControlf("close active pid file %q: %v", path, closeErr)
		}
		return true, nil
	}

	// stop 临时取得空闲锁后立即释放，不能截断、写入或取得后续资源所有权。
	if releaseErr := processlock.Release(file); releaseErr != nil {
		return false, processControlf("release observed pid lock %q: %v", path, releaseErr)
	}
	return false, nil
}
