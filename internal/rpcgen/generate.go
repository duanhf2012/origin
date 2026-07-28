package rpcgen

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"
)

// Options 配置一次完整、原子化的 RPC 生成。
type Options struct {
	Patterns []string
	Check    bool
	Dir      string
}

// Run 扫描指定 Build Context，并在全部验证通过后一次性提交生成文件。
func Run(options Options) error {
	if len(options.Patterns) == 0 {
		return errors.New("origingen rpc 至少需要一个 Go 包模式")
	}
	loadConfig := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedImports |
			packages.NeedDeps |
			packages.NeedModule,
		Dir: options.Dir,
	}
	overlay, err := generatedOverlay(options.Dir)
	if err != nil {
		return err
	}
	loadConfig.Overlay = overlay
	loaded, err := packages.Load(loadConfig, options.Patterns...)
	if err != nil {
		return fmt.Errorf("加载 Go 包: %w", err)
	}
	if count := packages.PrintErrors(loaded); count > 0 {
		return fmt.Errorf("加载 Go 包失败，共 %d 个错误", count)
	}

	scanned := rootAndModulePackages(loaded)
	// 自定义 Codec 必须先于契约收集完成冻结，使后续全类型图验证和指纹使用同一目录。
	codecs, err := collectCustomCodecs(scanned)
	if err != nil {
		return err
	}
	contracts, err := collectContracts(scanned, codecs)
	if err != nil {
		return err
	}
	services, err := collectServiceBindings(scanned, contracts)
	if err != nil {
		return err
	}
	outputs := groupOutputs(scanned, contracts, services)

	rendered := make(map[string][]byte, len(outputs))
	for _, output := range outputs {
		directory, err := packageDirectory(output.pkg)
		if err != nil {
			return err
		}
		content, err := renderPackage(output)
		if err != nil {
			return err
		}
		rendered[filepath.Join(directory, generatedFileName)] = content
	}
	return commitGenerated(scanned, rendered, options.Check)
}

// generatedOverlay 在类型检查时把旧生成文件替换成仅含 package 子句的内存版本。
//
// RPC 契约发生变化后，旧文件很可能暂时无法编译；生成器必须仍能重新生成，而不能要求
// 使用者手工删除文件。Overlay 不修改磁盘，最终仍由 commitGenerated 原子比较和替换。
func generatedOverlay(directory string) (map[string][]byte, error) {
	if directory == "" {
		var err error
		directory, err = os.Getwd()
		if err != nil {
			return nil, err
		}
	}
	root := directory
	for {
		if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(root)
		if parent == root {
			return nil, fmt.Errorf("%s: 找不到 go.mod", directory)
		}
		root = parent
	}

	overlay := make(map[string][]byte)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Name() != generatedFileName {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !bytes.HasPrefix(content, []byte(generatedMarker)) {
			return nil
		}
		file, err := parser.ParseFile(
			token.NewFileSet(),
			path,
			content,
			parser.PackageClauseOnly,
		)
		if err != nil {
			return fmt.Errorf("%s: 读取旧生成文件 package: %w", path, err)
		}
		overlay[path] = []byte("package " + file.Name.Name + "\n")
		return nil
	})
	return overlay, err
}

// rootAndModulePackages 只生成当前扫描根所在 Module 的包，依赖包仅用于类型检查。
func rootAndModulePackages(roots []*packages.Package) []*packages.Package {
	modulePaths := make(map[string]struct{})
	for _, root := range roots {
		if root.Module != nil {
			modulePaths[root.Module.Path] = struct{}{}
		}
	}
	seen := make(map[string]bool)
	var result []*packages.Package
	var visit func(*packages.Package)
	visit = func(pkg *packages.Package) {
		if pkg == nil || seen[pkg.ID] {
			return
		}
		seen[pkg.ID] = true
		if pkg.Module != nil {
			if _, allowed := modulePaths[pkg.Module.Path]; allowed {
				result = append(result, pkg)
			}
		}
		for _, imported := range pkg.Imports {
			visit(imported)
		}
	}
	for _, root := range roots {
		visit(root)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].PkgPath < result[j].PkgPath
	})
	return result
}

// groupOutputs 按 Go 包聚合契约与 Service 适配器，并冻结文件生成顺序。
func groupOutputs(
	scanned []*packages.Package,
	contracts []*contract,
	services []serviceBinding,
) []packageOutput {
	// 同一个包的契约和实现适配器必须写入唯一生成文件，避免多个生成步骤互相覆盖。
	byPath := make(map[string]*packageOutput)
	find := func(pkg *packages.Package) *packageOutput {
		output := byPath[pkg.PkgPath]
		if output == nil {
			output = &packageOutput{pkg: pkg}
			byPath[pkg.PkgPath] = output
		}
		return output
	}
	for _, item := range contracts {
		find(item.pkg).contracts = append(find(item.pkg).contracts, item)
	}
	for _, binding := range services {
		find(binding.pkg).services = append(find(binding.pkg).services, binding)
	}
	result := make([]packageOutput, 0, len(byPath))
	for _, output := range byPath {
		sort.Slice(output.contracts, func(i, j int) bool {
			return output.contracts[i].name < output.contracts[j].name
		})
		sort.Slice(output.services, func(i, j int) bool {
			return output.services[i].typeName < output.services[j].typeName
		})
		result = append(result, *output)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].pkg.PkgPath < result[j].pkg.PkgPath
	})
	_ = scanned
	return result
}

// importSet 为单个生成文件分配稳定且无冲突的导入别名。
type importSet struct {
	currentPath string
	byPath      map[string]string
	used        map[string]bool
}

// newImportSet 创建一个尚未分配别名的文件级导入表。
func newImportSet(currentPath string) *importSet {
	return &importSet{
		currentPath: currentPath,
		byPath:      make(map[string]string),
		used:        make(map[string]bool),
	}
}

// add 为导入路径分配稳定别名；重复路径复用，名称冲突按数字后缀确定性解决。
func (imports *importSet) add(path, preferred string) string {
	if path == imports.currentPath {
		return ""
	}
	if existing := imports.byPath[path]; existing != "" {
		return existing
	}
	base := preferred
	if base == "" {
		base = filepath.Base(path)
	}
	base = strings.ReplaceAll(base, "-", "_")
	alias := base
	for suffix := 2; imports.used[alias]; suffix++ {
		alias = base + strconv.Itoa(suffix)
	}
	imports.used[alias] = true
	imports.byPath[path] = alias
	return alias
}

// qualifier 实现 types.TypeString 的包限定回调，并复用当前文件的导入表。
func (imports *importSet) qualifier(pkg *types.Package) string {
	if pkg == nil || pkg.Path() == imports.currentPath {
		return ""
	}
	return imports.add(pkg.Path(), pkg.Name())
}

// typeName 返回可直接写入当前生成文件的完整类型表达式。
func (imports *importSet) typeName(typ types.Type) string {
	return types.TypeString(typ, imports.qualifier)
}

// block 按路径排序输出确定性的 import 块。
func (imports *importSet) block() string {
	if len(imports.byPath) == 0 {
		return ""
	}
	paths := make([]string, 0, len(imports.byPath))
	for path := range imports.byPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var builder strings.Builder
	builder.WriteString("import (\n")
	for _, path := range paths {
		fmt.Fprintf(
			&builder,
			"\t%s %q\n",
			imports.byPath[path],
			path,
		)
	}
	builder.WriteString(")\n\n")
	return builder.String()
}

// renderPackage 把一个包的全部契约与 Service 适配器渲染为单个 gofmt 结果。
func renderPackage(output packageOutput) ([]byte, error) {
	imports := newImportSet(output.pkg.PkgPath)
	// 只有声明契约的包会生成带 Context 的客户端和 Dispatcher。纯 Service 实现包只
	// 生成跨包适配器，提前导入 context 会让常见的契约/实现分包布局无法编译。
	contextAlias := ""
	if len(output.contracts) > 0 {
		contextAlias = imports.add("context", "context")
	}
	rpcAlias := imports.add("github.com/duanhf2012/origin/v3/rpc", "rpc")
	var body bytes.Buffer

	// 两个无符号常量只在 Runtime ABI 恰好等于当前生成器版本 1 时都非负。单向减法
	// 只能发现降级，无法发现 Runtime 升级，因此必须同时检查两个方向。
	fmt.Fprintf(
		&body,
		"const (\n"+
			"\t_ uint = %s.GeneratedABIVersion - 1\n"+
			"\t_ uint = 1 - %s.GeneratedABIVersion\n"+
			")\n\n",
		rpcAlias,
		rpcAlias,
	)
	for _, item := range output.contracts {
		if err := renderContract(
			&body,
			imports,
			contextAlias,
			rpcAlias,
			item,
		); err != nil {
			return nil, err
		}
	}
	for _, binding := range output.services {
		contractAlias := imports.qualifier(binding.contract.pkg.Types)
		constructor := "New" + binding.contract.name + "Dispatcher"
		if contractAlias != "" {
			constructor = contractAlias + "." + constructor
		}
		fmt.Fprintf(
			&body,
			"// RPCDispatcher 由 origingen 生成，使 Node 无需业务注册样板即可发现 %s。\n"+
				"func (service *%s) RPCDispatcher() %s.Dispatcher {\n"+
				"\treturn %s(service)\n"+
				"}\n\n",
			binding.contract.fullName,
			binding.typeName,
			rpcAlias,
			constructor,
		)
	}

	var source bytes.Buffer
	source.WriteString(generatedMarker)
	source.WriteString("\n\npackage ")
	source.WriteString(output.pkg.Name)
	source.WriteString("\n\n")
	source.WriteString(imports.block())
	source.Write(body.Bytes())
	formatted, err := format.Source(source.Bytes())
	if err != nil {
		return nil, fmt.Errorf(
			"%s: 格式化生成代码失败: %w\n%s",
			output.pkg.PkgPath,
			err,
			source.String(),
		)
	}
	return formatted, nil
}

// commitGenerated 比较全部目标和旧生成文件后再执行写入，避免校验失败留下半套代码。
func commitGenerated(
	scanned []*packages.Package,
	rendered map[string][]byte,
	check bool,
) error {
	// 目标文件名可能被业务手工占用。生成器只拥有带完整标记的文件，任何未标记文件都
	// 必须在比较和写入前失败，不能因为名称碰巧相同而覆盖使用者代码。
	for path := range rendered {
		content, err := os.ReadFile(path)
		switch {
		case err == nil && !bytes.HasPrefix(content, []byte(generatedMarker)):
			return fmt.Errorf("%s: 目标文件存在但不是 origingen 生成文件", path)
		case err != nil && !os.IsNotExist(err):
			return err
		}
	}

	// 收集当前扫描范围内由 origingen 拥有的旧文件，供过期比较和安全删除使用。
	existing := make(map[string][]byte)
	for _, pkg := range scanned {
		if len(pkg.GoFiles) == 0 {
			continue
		}
		path := filepath.Join(filepath.Dir(pkg.GoFiles[0]), generatedFileName)
		content, err := os.ReadFile(path)
		if err == nil && bytes.HasPrefix(content, []byte(generatedMarker)) {
			existing[path] = content
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	var changes []string
	for path, content := range rendered {
		if !bytes.Equal(existing[path], content) {
			changes = append(changes, path)
		}
		delete(existing, path)
	}
	for path := range existing {
		changes = append(changes, path)
	}
	sort.Strings(changes)
	if check && len(changes) > 0 {
		return fmt.Errorf(
			"RPC 生成文件不是最新状态:\n%s",
			strings.Join(changes, "\n"),
		)
	}
	if check {
		return nil
	}

	for path, content := range rendered {
		if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, content) {
			continue
		}
		temporary := path + ".tmp"
		if err := os.WriteFile(temporary, content, 0o644); err != nil {
			return err
		}
		if err := os.Rename(temporary, path); err != nil {
			_ = os.Remove(temporary)
			return err
		}
	}
	for path := range existing {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return nil
}
