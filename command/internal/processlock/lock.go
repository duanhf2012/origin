// Package processlock 封装各支持平台上的非阻塞 PID 文件独占锁。
//
// 该包只负责操作已经打开的文件句柄，不负责创建、截断、读取或删除 PID 文件。
// 文件生命周期始终由上层 command 包持有，避免锁实现同时拥有第二套文件关闭路径。
package processlock

import (
	"errors"
	"os"
)

// Release 依次释放文件锁并关闭文件，返回两个清理步骤的组合错误。
//
// 调用方只能对已经成功获得锁的文件调用 Release。即使解锁失败，本函数仍会继续关闭
// 文件句柄，让操作系统在进程内尽早回收资源。
func Release(file *os.File) error {
	// 先显式解锁，便于正常路径及时把运行权交给等待中的 stop 或下一次 start。
	unlockErr := Unlock(file)
	// 无论解锁是否成功都关闭句柄；操作系统关闭句柄也是异常清理时的最终释放保障。
	closeErr := file.Close()
	return errors.Join(unlockErr, closeErr)
}
