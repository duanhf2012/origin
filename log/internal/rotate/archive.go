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

type archiveFile struct {
	path    string
	modTime time.Time
}

// Maintain 清理临时文件、压缩归档并应用期限和数量规则。
func Maintain(config Config) error {
	archives, err := scanArchives(config.Path)
	if err != nil {
		return err
	}

	var result error
	for _, archive := range archives {
		if strings.HasSuffix(archive.path, ".gz.tmp") {
			if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, err)
			}
		}
	}

	if config.Compress {
		archives, err = scanArchives(config.Path)
		if err != nil {
			return errors.Join(result, err)
		}
		for _, archive := range archives {
			if strings.HasSuffix(archive.path, ".gz") ||
				strings.HasSuffix(archive.path, ".gz.tmp") {
				continue
			}
			if err := compressArchive(archive.path); err != nil {
				result = errors.Join(result, err)
			}
		}
	}

	archives, err = scanArchives(config.Path)
	if err != nil {
		return errors.Join(result, err)
	}
	now := time.Now()
	if config.Now != nil {
		now = config.Now()
	}

	kept := archives[:0]
	for _, archive := range archives {
		if strings.HasSuffix(archive.path, ".gz.tmp") {
			continue
		}
		if config.MaxAge > 0 && now.Sub(archive.modTime) > config.MaxAge {
			if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, err)
				kept = append(kept, archive)
			}
			continue
		}
		kept = append(kept, archive)
	}

	sort.Slice(kept, func(left, right int) bool {
		if kept[left].modTime.Equal(kept[right].modTime) {
			return kept[left].path > kept[right].path
		}
		return kept[left].modTime.After(kept[right].modTime)
	})
	if config.MaxFiles > 0 && len(kept) > config.MaxFiles {
		for _, archive := range kept[config.MaxFiles:] {
			if err := os.Remove(archive.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, err)
			}
		}
	}
	return result
}

func scanArchives(active string) ([]archiveFile, error) {
	directory := filepath.Dir(active)
	extension := filepath.Ext(active)
	stem := strings.TrimSuffix(filepath.Base(active), extension)
	prefix := stem + "-"

	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	archives := make([]archiveFile, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !isArchiveName(name, prefix, extension) {
			continue
		}
		if !strings.HasSuffix(name, extension) &&
			!strings.HasSuffix(name, extension+".gz") &&
			!strings.HasSuffix(name, extension+".gz.tmp") {
			continue
		}
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

func compressArchive(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	source, err := os.Open(path)
	if err != nil {
		return err
	}

	temporary := path + ".gz.tmp"
	target, err := os.OpenFile(temporary, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		_ = source.Close()
		return err
	}

	gzipWriter := gzip.NewWriter(target)
	_, copyErr := io.Copy(gzipWriter, source)
	gzipErr := gzipWriter.Close()
	syncErr := target.Sync()
	targetErr := target.Close()
	sourceErr := source.Close()
	if err := errors.Join(copyErr, gzipErr, syncErr, targetErr, sourceErr); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, path+".gz"); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Chtimes(path+".gz", info.ModTime(), info.ModTime()); err != nil {
		_ = os.Remove(path + ".gz")
		return err
	}
	if err := os.Remove(path); err != nil {
		_ = os.Remove(path + ".gz")
		return err
	}
	return nil
}

func isArchiveName(name, prefix, extension string) bool {
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
	if len(value) < len("2006-01-02T15-04-05.000") {
		return false
	}
	timestamp := value[:len("2006-01-02T15-04-05.000")]
	if _, err := time.Parse("2006-01-02T15-04-05.000", timestamp); err != nil {
		return false
	}
	if len(value) == len(timestamp) {
		return true
	}
	if value[len(timestamp)] != '-' {
		return false
	}
	sequence, err := strconv.Atoi(value[len(timestamp)+1:])
	return err == nil && sequence > 0
}
