package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/duanhf2012/origin/v3/errs"
)

type configFile struct {
	path     string
	relative string
	format   string
}

func scanDir(dir string) ([]configFile, error) {
	if dir == "" {
		return nil, invalidArgument("配置目录不能为空")
	}

	absoluteRoot, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return nil, invalidArgument("配置目录路径无效")
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, invalidConfig("无法访问配置目录 " + absoluteRoot + ": " + err.Error())
	}
	if !info.IsDir() {
		return nil, invalidArgument("配置路径必须是目录: " + absoluteRoot)
	}

	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return nil, invalidConfig("无法解析配置目录 " + absoluteRoot + ": " + err.Error())
	}

	files := make([]configFile, 0, 16)
	seen := make(map[string]string)
	err = filepath.WalkDir(absoluteRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == absoluteRoot {
			return nil
		}
		if entry.IsDir() {
			return nil
		}

		if entry.Type()&os.ModeSymlink != 0 {
			info, err := validateFileLink(path, resolvedRoot)
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
		} else {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
		}

		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".json" && extension != ".yml" && extension != ".yaml" {
			return nil
		}

		relative, err := filepath.Rel(absoluteRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(filepath.Clean(relative))
		folded := strings.ToLower(relative)
		if previous, exists := seen[folded]; exists {
			return invalidConfig("配置文件路径忽略大小写后冲突: " + previous + " 与 " + relative)
		}
		seen[folded] = relative
		files = append(files, configFile{
			path:     path,
			relative: relative,
			format:   extension,
		})
		return nil
	})
	if err != nil {
		if isOriginError(err) {
			return nil, err
		}
		return nil, invalidConfig("扫描配置目录 " + absoluteRoot + " 失败: " + err.Error())
	}
	if len(files) == 0 {
		return nil, invalidConfig("配置目录中没有 JSON/YAML 文件: " + absoluteRoot)
	}

	sort.Slice(files, func(left, right int) bool {
		return files[left].relative < files[right].relative
	})
	return files, nil
}

func validateFileLink(path, resolvedRoot string) (fs.FileInfo, error) {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, invalidConfig("无法解析配置文件链接 " + path + ": " + err.Error())
	}
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, invalidConfig("配置文件链接目标无效 " + path + ": " + err.Error())
	}
	relative, err := filepath.Rel(resolvedRoot, absoluteTarget)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, invalidConfig("配置文件链接越出配置目录: " + path)
	}
	info, err := os.Stat(absoluteTarget)
	if err != nil {
		return nil, invalidConfig("无法访问配置文件链接目标 " + path + ": " + err.Error())
	}
	if !info.Mode().IsRegular() && !info.IsDir() {
		return nil, invalidConfig("配置文件链接目标不是普通文件: " + path)
	}
	return info, nil
}

func isOriginError(err error) bool {
	var coder errs.Coder
	return errors.As(err, &coder)
}
