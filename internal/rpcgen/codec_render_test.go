package rpcgen

import (
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
)

// TestRenderSizeMarksFixedSliceIndexUsed 覆盖 Slice 元素只包含固定宽度字段时，
// Size 阶段仍必须生成可编译代码，不能留下未使用的 range 下标。
func TestRenderSizeMarksFixedSliceIndexUsed(t *testing.T) {
	pkgTypes := types.NewPackage("example.com/game/contract", "contract")
	fixed := types.NewNamed(
		types.NewTypeName(token.NoPos, pkgTypes, "Fixed", nil),
		types.NewStruct(
			[]*types.Var{
				types.NewField(token.NoPos, pkgTypes, "Code", types.Typ[types.Int32], false),
			},
			nil,
		),
		nil,
	)
	request := types.NewNamed(
		types.NewTypeName(token.NoPos, pkgTypes, "Request", nil),
		types.NewStruct(
			[]*types.Var{
				types.NewField(
					token.NoPos,
					pkgTypes,
					"Items",
					types.NewSlice(fixed),
					false,
				),
			},
			nil,
		),
		nil,
	)
	item := &contract{
		pkg: &packages.Package{
			PkgPath: "example.com/game/contract",
			Name:    "contract",
			Types:   pkgTypes,
		},
		name:        "FixedService",
		fullName:    "example.com/game/contract.FixedService",
		id:          1,
		fingerprint: [32]byte{1},
		methods: []*method{{
			name:   "Store",
			id:     2,
			inputs: []parameter{{name: "arg1", typ: request}},
		}},
	}
	content, err := renderPackage(packageOutput{
		pkg:       item.pkg,
		contracts: []*contract{item},
	})
	if err != nil {
		t.Fatalf("renderPackage() error = %v", err)
	}
	source := string(content)
	if !strings.Contains(source, "for index") || !strings.Contains(source, "_ = index") {
		t.Fatalf("固定宽度 Slice 的 Size 循环没有标记下标已使用:\n%s", source)
	}
}
