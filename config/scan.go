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

// configFile 描述扫描阶段确认可读取的单个逻辑配置文件。
type configFile struct {
	// path 是实际读取使用的绝对或清理后系统路径。
	path string
	// relative 是排序和错误输出使用的斜杠相对路径。
	relative string
	// format 是已经转为小写的文件扩展名。
	format string
}

// scanDir 验证配置根目录并返回稳定排序的支持文件列表。
func scanDir(dir string) ([]configFile, error) {
	// 空字符串通常代表调用方遗漏配置参数，归类为参数错误。
	if dir == "" {
		return nil, invalidArgument("配置目录不能为空")
	}

	// 先清理并绝对化入口，使后续边界和错误路径不依赖进程工作目录变化。
	absoluteRoot, err := filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return nil, invalidArgument("配置目录路径无效")
	}
	// Stat 跟随入口符号链接，并确认调用方传入的是可访问目录。
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, invalidConfig("无法访问配置目录 " + absoluteRoot + ": " + err.Error())
	}
	if !info.IsDir() {
		return nil, invalidArgument("配置路径必须是目录: " + absoluteRoot)
	}

	// 保存真实根路径，文件符号链接最终目标必须仍落在这个边界内。
	resolvedRoot, err := filepath.EvalSymlinks(absoluteRoot)
	if err != nil {
		return nil, invalidConfig("无法解析配置目录 " + absoluteRoot + ": " + err.Error())
	}

	// 预分配常见小配置目录容量，并记录大小写折叠后的逻辑路径。
	files := make([]configFile, 0, 16)
	seen := make(map[string]string)
	// WalkDir 不跟随目录符号链接，天然避免递归进入外部目录或链接环。
	err = filepath.WalkDir(absoluteRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		// 文件系统遍历错误必须中止，不能静默得到不完整配置。
		if walkErr != nil {
			return walkErr
		}
		// 根目录本身不作为候选文件处理。
		if path == absoluteRoot {
			return nil
		}
		// 普通目录交给 WalkDir 继续递归。
		if entry.IsDir() {
			return nil
		}

		// 文件链接需要额外确认最终目标类型和真实根目录边界。
		if entry.Type()&os.ModeSymlink != 0 {
			info, err := validateFileLink(path, resolvedRoot)
			if err != nil {
				return err
			}
			if info.IsDir() {
				// 指向目录的链接不递归，也不作为配置文件读取。
				return nil
			}
		} else {
			// 非链接条目只接受普通文件，忽略设备、管道等特殊对象。
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return nil
			}
		}

		// 扩展名大小写不敏感，但不对内容格式进行猜测或回退。
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".json" && extension != ".yml" && extension != ".yaml" {
			return nil
		}

		// 把逻辑路径规范为跨平台斜杠形式，作为唯一排序和来源标识。
		relative, err := filepath.Rel(absoluteRoot, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(filepath.Clean(relative))
		// 主动拒绝大小写折叠冲突，确保 Windows 与 Linux 得到同一文件集合。
		folded := strings.ToLower(relative)
		if previous, exists := seen[folded]; exists {
			return invalidConfig("配置文件路径忽略大小写后冲突: " + previous + " 与 " + relative)
		}
		// 校验完成后登记文件；真正读取留给 parseFile。
		seen[folded] = relative
		files = append(files, configFile{
			path:     path,
			relative: relative,
			format:   extension,
		})
		return nil
	})
	if err != nil {
		// 已经带 Origin 错误码的边界错误原样返回，避免重复包装丢失消息。
		if isOriginError(err) {
			return nil, err
		}
		// 原始文件系统错误补充扫描根目录上下文。
		return nil, invalidConfig("扫描配置目录 " + absoluteRoot + " 失败: " + err.Error())
	}
	// 没有支持格式时显式失败，防止空配置被误认为完整默认配置。
	if len(files) == 0 {
		return nil, invalidConfig("配置目录中没有 JSON/YAML 文件: " + absoluteRoot)
	}

	// 全部候选收集完后统一排序，文件系统枚举顺序不进入配置语义。
	sort.Slice(files, func(left, right int) bool {
		return files[left].relative < files[right].relative
	})
	return files, nil
}

// validateFileLink 检查一个逻辑配置文件链接不会逃逸真实配置根目录。
func validateFileLink(path, resolvedRoot string) (fs.FileInfo, error) {
	// EvalSymlinks 同时发现链接环、失效链接并返回最终目标。
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, invalidConfig("无法解析配置文件链接 " + path + ": " + err.Error())
	}
	// 绝对化最终目标，后续 filepath.Rel 才能可靠判断包含关系。
	absoluteTarget, err := filepath.Abs(target)
	if err != nil {
		return nil, invalidConfig("配置文件链接目标无效 " + path + ": " + err.Error())
	}
	// WalkDir 本来就不会递归目录链接；先识别最终类型，确保根内或根外的目录链接都按
	// “不跟随、不读取”规则安全忽略，而不会被误判成越界配置文件。
	info, err := os.Stat(absoluteTarget)
	if err != nil {
		return nil, invalidConfig("无法访问配置文件链接目标 " + path + ": " + err.Error())
	}
	if info.IsDir() {
		return info, nil
	}
	// 相对路径为 .. 或以 ../ 开头表示目标越过真实根边界。
	relative, err := filepath.Rel(resolvedRoot, absoluteTarget)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, invalidConfig("配置文件链接越出配置目录: " + path)
	}
	// 非目录链接仍必须指向根目录内的普通文件，设备和管道不能进入解析层。
	if !info.Mode().IsRegular() {
		return nil, invalidConfig("配置文件链接目标不是普通文件: " + path)
	}
	// 返回目标信息供调用方区分文件链接和目录链接。
	return info, nil
}

// isOriginError 报告错误链中是否已经包含稳定 Origin 错误码。
func isOriginError(err error) bool {
	// errors.As 支持错误被其他文件系统上下文包装的情况。
	var coder errs.Coder
	return errors.As(err, &coder)
}
