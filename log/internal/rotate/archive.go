package rotate

import (
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// archiveFile 保存一次扫描得到的归档路径和保留排序时间。
type archiveFile struct {
	path    string
	modTime time.Time
}

// Maintain 清理临时文件、压缩归档并应用期限和数量规则。
func Maintain(config Config) error {
	// 第一次扫描建立当前归档快照；扫描失败时不能安全继续删除。
	archives, err := scanArchives(config.Path)
	if err != nil {
		return err
	}

	// 先删除上次崩溃遗留的压缩临时文件，避免它们永久占用归档名称。
	var result error
	for _, archive := range archives {
		if strings.HasSuffix(archive.path, ".gz.tmp") {
			if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, err)
			}
		}
	}

	// 启用压缩时重新扫描，确保刚删除的临时文件不留在快照中。
	if config.Compress {
		archives, err = scanArchives(config.Path)
		if err != nil {
			return errors.Join(result, err)
		}
		// 只压缩普通归档；已压缩和临时文件不能再次处理。
		for _, archive := range archives {
			if strings.HasSuffix(archive.path, ".gz") ||
				strings.HasSuffix(archive.path, ".gz.tmp") {
				continue
			}
			// 单个归档失败不阻止其他归档维护，最后统一返回错误。
			if err := compressArchive(archive.path); err != nil {
				result = errors.Join(result, err)
			}
		}
	}

	// 压缩可能改变文件集合，保留策略必须基于最新快照。
	archives, err = scanArchives(config.Path)
	if err != nil {
		return errors.Join(result, err)
	}
	// 维护过程只读取一次当前时间，保证同批归档年龄判断一致。
	now := time.Now()
	if config.Now != nil {
		now = config.Now()
	}

	// 先应用时间期限，并保留删除失败的文件供数量规则继续计算。
	kept := archives[:0]
	for _, archive := range archives {
		if strings.HasSuffix(archive.path, ".gz.tmp") {
			// 临时文件已在第一阶段尝试删除，不进入正式保留集合。
			continue
		}
		if config.MaxAge > 0 && now.Sub(archive.modTime) > config.MaxAge {
			if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, err)
				// 删除失败的归档仍实际存在，必须保留在后续数量计算中。
				kept = append(kept, archive)
			}
			continue
		}
		kept = append(kept, archive)
	}

	// 按修改时间从新到旧稳定排序，同时间用路径倒序打破平局。
	sort.Slice(kept, func(left, right int) bool {
		if kept[left].modTime.Equal(kept[right].modTime) {
			return kept[left].path > kept[right].path
		}
		return kept[left].modTime.After(kept[right].modTime)
	})
	// 数量限制只删除排序后超出最新 N 个的尾部归档。
	if config.MaxFiles > 0 && len(kept) > config.MaxFiles {
		for _, archive := range kept[config.MaxFiles:] {
			if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, err)
			}
		}
	}
	// 返回所有可恢复维护错误，不因其中一个失败丢失其他信息。
	return result
}

// scanArchives 只返回当前活动文件对应且命名合法的归档。
func scanArchives(active string) ([]archiveFile, error) {
	// 从活动文件拆出目录、扩展名和归档名前缀。
	directory := filepath.Dir(active)
	extension := filepath.Ext(active)
	stem := strings.TrimSuffix(filepath.Base(active), extension)
	prefix := stem + "-"

	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		// 目录尚未创建等价于没有归档。
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	// 预分配到目录项上限，扫描后切片通常更短。
	archives := make([]archiveFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		// 先用廉价条件排除目录、其他日志和不合法时间名称。
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !isArchiveName(name, prefix, extension) {
			continue
		}
		// 只接受普通、gzip 和 gzip 临时三种归档后缀。
		if !strings.HasSuffix(name, extension) &&
			!strings.HasSuffix(name, extension+".gz") &&
			!strings.HasSuffix(name, extension+".gz.tmp") {
			continue
		}
		// 修改时间用于期限判断和数量排序。
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		archives = append(archives, archiveFile{
			path:    filepath.Join(directory, name),
			modTime: info.ModTime(),
		})
	}
	return archives, nil
}

// compressArchive 以临时文件方式安全地把单个普通归档转换为 gzip。
func compressArchive(path string) error {
	// 先读取源文件时间，成功压缩后需要保留它用于保留策略。
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// 打开源文件；当前函数从此负责关闭它。
	source, err := os.Open(path)
	if err != nil {
		return err
	}

	// 先写 .gz.tmp，避免中途崩溃留下看似完整的 .gz。
	temporary := path + ".gz.tmp"
	target, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		// 目标创建失败前需要释放已经打开的源文件。
		_ = source.Close()
		return err
	}

	// 完成复制、gzip 尾部、磁盘刷新以及两个句柄关闭，汇总全部错误。
	gzipWriter := gzip.NewWriter(target)
	_, copyErr := io.Copy(gzipWriter, source)
	gzipErr := gzipWriter.Close()
	syncErr := target.Sync()
	targetErr := target.Close()
	sourceErr := source.Close()
	if err := errors.Join(copyErr, gzipErr, syncErr, targetErr, sourceErr); err != nil {
		// 任一步失败都删除临时文件，普通源归档保持不变。
		_ = os.Remove(temporary)
		return err
	}
	// 临时文件完整落盘后原子改名为最终 gzip 路径。
	if err := os.Rename(temporary, path+".gz"); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	// 恢复源文件修改时间，避免压缩时间改变归档年龄和排序。
	if err := os.Chtimes(path+".gz", info.ModTime(), info.ModTime()); err != nil {
		_ = os.Remove(path + ".gz")
		return err
	}
	// 只有 gzip 完整可用后才删除普通源归档。
	if err := os.Remove(path); err != nil {
		// 删除源失败时撤销 gzip，避免同一归档出现两份并扰乱计数。
		_ = os.Remove(path + ".gz")
		return err
	}
	return nil
}

// isArchiveName 严格验证归档时间戳和可选正整数序号。
func isArchiveName(name, prefix, extension string) bool {
	// 先移除活动文件前缀，再按最长后缀优先剥离压缩形式。
	value := strings.TrimPrefix(name, prefix)
	switch {
	case strings.HasSuffix(value, extension+".gz.tmp"):
		value = strings.TrimSuffix(value, extension+".gz.tmp")
	case strings.HasSuffix(value, extension+".gz"):
		value = strings.TrimSuffix(value, extension+".gz")
	case strings.HasSuffix(value, extension):
		value = strings.TrimSuffix(value, extension)
	default:
		return false
	}
	// 名称主体至少要容纳固定毫秒时间格式。
	if len(value) < len("2006-01-02T15-04-05.000") {
		return false
	}
	// 使用 time.Parse 验证日期与时间范围，而不仅检查字符形状。
	timestamp := value[:len("2006-01-02T15-04-05.000")]
	if _, err := time.Parse("2006-01-02T15-04-05.000", timestamp); err != nil {
		return false
	}
	// 没有剩余字符时是最初归档名。
	if len(value) == len(timestamp) {
		return true
	}
	// 冲突序号必须由连字符分隔。
	if value[len(timestamp)] != '-' {
		return false
	}
	// 只接受正整数序号；零和非数字后缀均不是本 Writer 归档。
	sequence, err := strconv.Atoi(value[len(timestamp)+1:])
	return err == nil && sequence > 0
}
