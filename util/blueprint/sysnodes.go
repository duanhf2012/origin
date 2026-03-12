package blueprint

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/duanhf2012/origin/v2/log"
)

// 系统入口ID定义，1000以内
const (
	EntranceID_IntParam   = 1
	EntranceID_ArrayParam = 2
	EntranceID_Timer      = 3
)

type Entrance_ArrayParam struct {
	BaseExecNode
}

func (em *Entrance_ArrayParam) GetName() string {
	return "Entrance_ArrayParam"
}

func (em *Entrance_ArrayParam) Exec() (int, error) {
	return 0, nil
}

type Entrance_IntParam struct {
	BaseExecNode
}

func (em *Entrance_IntParam) GetName() string {
	return "Entrance_IntParam"
}

func (em *Entrance_IntParam) Exec() (int, error) {
	return 0, nil
}

type Entrance_Timer struct {
	BaseExecNode
}

func (em *Entrance_Timer) GetName() string {
	return "Entrance_Timer"
}

func (em *Entrance_Timer) Exec() (int, error) {
	return 0, nil
}

type DebugOutput struct {
	BaseExecNode
}

func (em *DebugOutput) GetName() string {
	return "DebugOutput"
}

func (em *DebugOutput) Exec() (int, error) {
	val, ok := em.GetInPortInt(1)
	if !ok {
		return 0, fmt.Errorf("output Exec inParam not found")
	}

	valStr, ok := em.GetInPortStr(2)
	if !ok {
		return 0, fmt.Errorf("output Exec inParam not found")
	}

	valArray, ok := em.GetInPortArray(3)
	if !ok {
		return 0, fmt.Errorf("output Exec inParam not found")
	}

	log.Debug("DebugOutput Exec", log.Any("param1", val), log.Any("param2", valStr), log.Any("param3", valArray))
	return 0, nil
}

type Sequence struct {
	BaseExecNode
}

func (em *Sequence) GetName() string {
	return "Sequence"
}

func (em *Sequence) Exec() (int, error) {
	for i := range em.outPort {
		if !em.outPort[i].IsPortExec() {
			break
		}

		err := em.DoNext(i)
		if err != nil {
			return -1, err
		}
	}

	return -1, nil
}

type ForeachIntArray struct {
	BaseExecNode
}

func (em *ForeachIntArray) GetName() string {
	return "ForeachIntArray"
}

func (em *ForeachIntArray) Exec() (int, error) {
	array, ok := em.GetInPortArray(1)
	if !ok {
		return 0, fmt.Errorf("ForeachIntArray Exec inParam 1 not found")
	}

	for i := range array {
		em.ExecContext.OutputPorts[2].SetInt(Port_Int(i))
		em.ExecContext.OutputPorts[3].SetInt(array[i].IntVal)
		err := em.DoNext(0)
		if err != nil {
			return -1, err
		}
	}

	err := em.DoNext(1)
	if err != nil {
		return -1, err
	}

	return -1, nil
}

type Foreach struct {
	BaseExecNode
}

func (em *Foreach) GetName() string {
	return "Foreach"
}

func (em *Foreach) Exec() (int, error) {
	startIndex, ok := em.ExecContext.InputPorts[1].GetInt()
	if !ok {
		return 0, fmt.Errorf("foreach Exec inParam not found")
	}
	endIndex, ok := em.ExecContext.InputPorts[2].GetInt()
	if !ok {
		return 0, fmt.Errorf("foreach Exec inParam not found")
	}

	for i := startIndex; i < endIndex; i++ {
		em.ExecContext.OutputPorts[2].SetInt(i)
		err := em.DoNext(0)
		if err != nil {
			return -1, err
		}
	}

	err := em.DoNext(1)
	if err != nil {
		return -1, err
	}

	return -1, nil
}

type GetArrayInt struct {
	BaseExecNode
}

func (em *GetArrayInt) GetName() string {
	return "GetArrayInt"
}

func (em *GetArrayInt) Exec() (int, error) {
	inPort := em.GetInPort(0)
	if inPort == nil {
		return -1, fmt.Errorf("GetArrayInt inParam not found")
	}
	outPort := em.GetOutPort(0)
	if outPort == nil {
		return -1, fmt.Errorf("GetArrayInt outParam not found")
	}

	arrIndexPort := em.GetInPort(1)
	if arrIndexPort == nil {
		return -1, fmt.Errorf("GetArrayInt arrIndexParam not found")
	}
	arrIndex, ok := arrIndexPort.GetInt()
	if !ok {
		return -1, fmt.Errorf("GetArrayInt arrIndexParam not found")
	}

	if arrIndex < 0 || arrIndex >= inPort.GetArrayLen() {
		return -1, fmt.Errorf("GetArrayInt arrIndexParam out of range,index %d", arrIndex)
	}

	val, ok := inPort.GetArrayValInt(int(arrIndex))
	if !ok {
		log.Errorf("GetArrayValInt failed, idx:%d", arrIndex)
		return -1, fmt.Errorf("GetArrayInt inParam not found")
	}

	outPort.SetInt(val)
	return -1, nil
}

type GetArrayString struct {
	BaseExecNode
}

func (em *GetArrayString) GetName() string {
	return "GetArrayString"
}

func (em *GetArrayString) Exec() (int, error) {
	inPort := em.GetInPort(0)
	if inPort == nil {
		return -1, fmt.Errorf("GetArrayInt inParam 0 not found")
	}
	outPort := em.GetOutPort(0)
	if outPort == nil {
		return -1, fmt.Errorf("GetArrayInt outParam 0 not found")
	}

	arrIndexPort := em.GetInPort(1)
	if arrIndexPort == nil {
		return -1, fmt.Errorf("GetArrayInt arrIndexParam 1 not found")
	}
	arrIndex, ok := arrIndexPort.GetInt()
	if !ok {
		return -1, fmt.Errorf("GetArrayInt arrIndexParam not found")
	}

	if arrIndex < 0 || arrIndex >= inPort.GetArrayLen() {
		return -1, fmt.Errorf("GetArrayInt arrIndexParam out of range,index %d", arrIndex)
	}

	val, ok := inPort.GetArrayValStr(int(arrIndex))
	if !ok {
		log.Errorf("GetArrayValStr failed, idx:%d", arrIndex)
		return -1, fmt.Errorf("GetArrayInt inParam not found")
	}

	outPort.SetStr(val)
	return -1, nil
}

type GetArrayLen struct {
	BaseExecNode
}

func (em *GetArrayLen) GetName() string {
	return "GetArrayLen"
}

func (em *GetArrayLen) Exec() (int, error) {
	inPort := em.GetInPort(0)
	if inPort == nil {
		return -1, fmt.Errorf("GetArrayInt inParam 0 not found")
	}
	outPort := em.GetOutPort(0)
	if outPort == nil {
		return -1, fmt.Errorf("GetArrayInt outParam 0 not found")
	}

	outPort.SetInt(inPort.GetArrayLen())
	return -1, nil
}

// BoolIf 布尔判断
type BoolIf struct {
	BaseExecNode
}

func (em *BoolIf) GetName() string {
	return "BoolIf"
}

func (em *BoolIf) Exec() (int, error) {
	inPort := em.GetInPort(1)
	if inPort == nil {
		return -1, fmt.Errorf("GetArrayInt inParam 1 not found")
	}

	ret, ok := inPort.GetBool()
	if !ok {
		return -1, fmt.Errorf("BoolIf inParam error")
	}

	if ret {
		return 1, nil
	}

	return 0, nil
}

// GreaterThanInteger 大于(整型) >
type GreaterThanInteger struct {
	BaseExecNode
}

func (em *GreaterThanInteger) GetName() string {
	return "GreaterThanInteger"
}

func (em *GreaterThanInteger) Exec() (int, error) {
	inPortEqual := em.GetInPort(1)
	if inPortEqual == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	inPortA := em.GetInPort(2)
	if inPortA == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	inPorB := em.GetInPort(3)
	if inPorB == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	ret, ok := inPortEqual.GetBool()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 error")
	}

	inA, ok := inPortA.GetInt()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 2 error")
	}
	inB, ok := inPorB.GetInt()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 3 error")
	}
	if ret {
		if inA >= inB {
			return 1, nil
		}
		return 0, nil
	}

	if inA > inB {
		return 1, nil
	}
	return 0, nil
}

// LessThanInteger 小于(整型) <
type LessThanInteger struct {
	BaseExecNode
}

func (em *LessThanInteger) GetName() string {
	return "LessThanInteger"
}

func (em *LessThanInteger) Exec() (int, error) {
	inPortEqual := em.GetInPort(1)
	if inPortEqual == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	inPortA := em.GetInPort(2)
	if inPortA == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	inPorB := em.GetInPort(3)
	if inPorB == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	ret, ok := inPortEqual.GetBool()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 error")
	}

	inA, ok := inPortA.GetInt()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 2 error")
	}
	inB, ok := inPorB.GetInt()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 3 error")
	}
	if ret {
		if inA <= inB {
			return 1, nil
		}
		return 0, nil
	}

	if inA < inB {
		return 1, nil
	}
	return 0, nil
}

// EqualInteger 等于(整型)==
type EqualInteger struct {
	BaseExecNode
}

func (em *EqualInteger) GetName() string {
	return "EqualInteger"
}

func (em *EqualInteger) Exec() (int, error) {

	inPortA := em.GetInPort(1)
	if inPortA == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	inPorB := em.GetInPort(2)
	if inPorB == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	inA, ok := inPortA.GetInt()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 2 error")
	}
	inB, ok := inPorB.GetInt()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 3 error")
	}

	if inA == inB {
		return 1, nil
	}
	return 0, nil
}

// RangeCompare 范围比较<=
type RangeCompare struct {
	BaseExecNode
}

func (em *RangeCompare) GetName() string {
	return "RangeCompare"
}

func (em *RangeCompare) Exec() (int, error) {
	inPortA := em.GetInPort(1)
	if inPortA == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	ret, ok := inPortA.GetInt()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 error")
	}

	intArray := em.execNode.GetInPortDefaultIntArrayValue(2)
	if intArray == nil {
		return 0, nil
	}

	for i := 0; i < len(intArray) && i < em.GetOutPortCount()-2; i++ {
		if ret <= intArray[i] {
			return i + 2, nil
		}
	}

	return 0, nil
}

// EqualSwitch 等于分支==
type EqualSwitch struct {
	BaseExecNode
}

func (em *EqualSwitch) GetName() string {
	return "EqualSwitch"
}

func (em *EqualSwitch) Exec() (int, error) {
	inPortA := em.GetInPort(1)
	if inPortA == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	ret, ok := inPortA.GetInt()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 error")
	}

	intArray := em.execNode.GetInPortDefaultIntArrayValue(2)
	if intArray == nil {
		return 0, nil
	}

	for i := 0; i < len(intArray) && i < em.GetOutPortCount()-2; i++ {
		if ret == intArray[i] {
			return i + 2, nil
		}
	}

	return 0, nil
}

// Probability 概率判断(万分比)
type Probability struct {
	BaseExecNode
}

func (em *Probability) GetName() string {
	return "Probability"
}

func (em *Probability) Exec() (int, error) {
	inPortProbability := em.GetInPort(1)
	if inPortProbability == nil {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 not found")
	}

	inProbability, ok := inPortProbability.GetInt()
	if !ok {
		return -1, fmt.Errorf("GreaterThanInteger inParam 1 error")
	}

	if inProbability > rand.Int64N(10000) {
		return 1, nil
	}

	return 0, nil
}

// CreateIntArray 创建整型数组
type CreateIntArray struct {
	BaseExecNode
}

func (em *CreateIntArray) GetName() string {
	return "CreateIntArray"
}

func (em *CreateIntArray) Exec() (int, error) {
	intArray := em.execNode.GetInPortDefaultIntArrayValue(0)
	if intArray == nil {
		return -1, fmt.Errorf("CreateIntArray inParam 0 not found")
	}

	outPort := em.GetOutPort(0)
	if outPort == nil {
		return -1, fmt.Errorf("GetArrayInt outParam 0 not found")
	}

	for _, v := range intArray {
		outPort.AppendArrayValInt(v)
	}

	return -1, nil
}

// CreateStringArray 创建字符串数组
type CreateStringArray struct {
	BaseExecNode
}

func (em *CreateStringArray) GetName() string {
	return "CreateStringArray"
}

func (em *CreateStringArray) Exec() (int, error) {
	intArray := em.execNode.GetInPortDefaultStringArrayValue(0)
	if intArray == nil {
		return -1, fmt.Errorf("CreateIntArray inParam 0 not found")
	}

	outPort := em.GetOutPort(0)
	if outPort == nil {
		return -1, fmt.Errorf("GetArrayInt outParam 0 not found")
	}

	for _, v := range intArray {
		outPort.AppendArrayValStr(v)
	}

	return -1, nil
}

// AppendIntegerToArray 数组追加整型
type AppendIntegerToArray struct {
	BaseExecNode
}

func (em *AppendIntegerToArray) GetName() string {
	return "AppendIntegerToArray"
}

func (em *AppendIntegerToArray) Exec() (int, error) {
	inPortArray := em.GetInPort(0)
	if inPortArray == nil {
		return -1, fmt.Errorf("AppendIntegerToArray inParam 0 not found")
	}

	inPortVal := em.GetInPort(1)
	if inPortVal == nil {
		return -1, fmt.Errorf("AppendIntegerToArray inParam 1 not found")
	}

	outPort := em.GetOutPort(0)
	if outPort == nil {
		return -1, fmt.Errorf("AppendIntegerToArray outParam 0 not found")
	}

	intArray, ok := inPortArray.GetArray()
	if !ok {
		return -1, fmt.Errorf("AppendIntegerToArray inParam 0 error")
	}

	intVal, ok := inPortVal.GetInt()
	if !ok {
		return -1, fmt.Errorf("AppendIntegerToArray inParam 1 error")
	}

	for i := range intArray {
		outPort.AppendArrayValInt(intArray[i].IntVal)
	}
	outPort.AppendArrayValInt(intVal)
	return -1, nil
}

// AppendStringToArray 数组追加字符串
type AppendStringToArray struct {
	BaseExecNode
}

func (em *AppendStringToArray) GetName() string {
	return "AppendStringToArray"
}

func (em *AppendStringToArray) Exec() (int, error) {
	inPortArray := em.GetInPort(0)
	if inPortArray == nil {
		return -1, fmt.Errorf("AppendStringToArray inParam 0 not found")
	}

	inPortVal := em.GetInPort(1)
	if inPortVal == nil {
		return -1, fmt.Errorf("AppendStringToArray inParam 1 not found")
	}

	outPort := em.GetOutPort(0)
	if outPort == nil {
		return -1, fmt.Errorf("AppendStringToArray outParam 0 not found")
	}

	intArray, ok := inPortArray.GetArray()
	if !ok {
		return -1, fmt.Errorf("AppendStringToArray inParam 0 error")
	}

	for i := range intArray {
		outPort.AppendArrayValStr(intArray[i].StrVal)
	}

	return -1, nil
}

// CreateTimer 创建定时器
type CreateTimer struct {
	BaseExecNode
}

func (em *CreateTimer) GetName() string {
	return "CreateTimer"
}

func (em *CreateTimer) Exec() (int, error) {
	delay, ok := em.GetInPortInt(0)
	if !ok {
		return -1, fmt.Errorf("CreateTimer inParam 0 error")
	}

	array, ok := em.GetInPortArray(1)
	if !ok {
		return -1, fmt.Errorf("CreateTimer inParam 1 error")
	}

	var timerId uint64
	graphID := em.gr.graphID
	em.gr.IBlueprintModule.SafeAfterFunc(&timerId, time.Duration(delay)*time.Millisecond, nil, func(timerId uint64, additionData interface{}) {
		err := em.gr.IBlueprintModule.TriggerEvent(graphID, EntranceID_Timer, array)
		if err != nil {
			log.Warnf("CreateTimer SafeAfterFunc error timerId:%d err:%v", timerId, err)
		}

		em.gr.IBlueprintModule.CancelTimerId(graphID, &timerId)
	})

	em.gr.mapTimerID[timerId] = struct{}{}

	outPort := em.GetOutPort(1)
	if outPort == nil {
		return -1, fmt.Errorf("CreateTimer outParam 1 not found")
	}

	outPort.SetInt(int64(timerId))
	return 0, nil
}

// CloseTimer 关闭定时器
type CloseTimer struct {
	BaseExecNode
}

func (em *CloseTimer) GetName() string {
	return "CloseTimer"
}

func (em *CloseTimer) Exec() (int, error) {
	timerID, ok := em.GetInPortInt(1)
	if !ok {
		return -1, fmt.Errorf("CreateTimer inParam 1 error")
	}

	id := uint64(timerID)
	ok = em.gr.IBlueprintModule.CancelTimerId(em.gr.graphID, &id)
	if !ok {
		log.Warnf("CloseTimer CancelTimerId:%d", id)
	}

	return 0, nil
}




// AppendIntReturn 追加返回结果(Int)
type AppendIntReturn struct {
	BaseExecNode
}

func (em *AppendIntReturn) GetName() string {
	return "AppendIntReturn"
}

func (em *AppendIntReturn) Exec() (int, error) {
	val, ok := em.GetInPortInt(1)
	if !ok {
		return -1, fmt.Errorf("AppendIntReturn inParam 1 error")
	}

	returnPort := em.gr.GetAndCreateReturnPort()
	if returnPort == nil {
		return -1, fmt.Errorf("GetAndCreateReturnPort fail")
	}
	returnPort.AppendArrayValInt(val)

	return -0, nil
}



// AppendStringReturn 追加返回结果(String)
type AppendStringReturn struct {
	BaseExecNode
}

func (em *AppendStringReturn) GetName() string {
	return "AppendStringReturn"
}

func (em *AppendStringReturn) Exec() (int, error) {
	val, ok := em.GetInPortStr(1)
	if !ok {
		return -1, fmt.Errorf("AppendStringReturn inParam 1 error")
	}

	returnPort := em.gr.GetAndCreateReturnPort()
	if returnPort == nil {
		return -1, fmt.Errorf("GetAndCreateReturnPort fail")
	}
	returnPort.AppendArrayValStr(val)

	return -0, nil
}

// IntInArray 判断整型值是否在整型数组中
type IntInArray struct {
	BaseExecNode
}

func (em *IntInArray) GetName() string {
	return "IntInArray"
}

func (em *IntInArray) Exec() (int, error) {
	val, ok := em.GetInPortInt(1)
	if !ok {
		return -1, fmt.Errorf("IntInArray inParam 1 not found")
	}

	array, ok := em.GetInPortArray(2)
	if !ok {
		return -1, fmt.Errorf("IntInArray inParam 0 not found")
	}

	bFind := false
	for i := range array {
		if array[i].IntVal == val {
			bFind = true
			break
		}
	}

	em.SetOutPortBool(1,bFind)
	return -0, nil
}