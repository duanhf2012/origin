package algorithms

import "testing"

func Test_Bitwise(t *testing.T) {
	//1.预分配10个byte切片，用于存储位标识
	byteBuff := make([]byte, 10)

	//2.获取buff总共位数
	bitNum := GetBitwiseNum(byteBuff)
	t.Log(bitNum)

	//3..对索引79位打标记，注意是从0开始，79即为最后一个位
	idx := uint(79)

	//4.对byteBuff索引idx位置打上标记
	SetBitwiseTag(byteBuff, idx)

	//5.获取索引idx位置标记
	isTag, ret := GetBitwiseTag(byteBuff, idx)
	t.Log("set index ", idx, "  :", isTag, ret)
	if isTag != true {
		t.Fatal("error")
	}

	//6.清除掉索引idx位标记
	ClearBitwiseTag(byteBuff, idx)

	//7.获取索引idx位置标记
	isTag, ret = GetBitwiseTag(byteBuff, idx)
	t.Log("get index ", idx, "  :", isTag, ret)

	if isTag != false {
		t.Fatal("error")
	}

}
