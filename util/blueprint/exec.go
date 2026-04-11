package blueprint

import "fmt"

// IInnerExecNode 配置生成的结点
type IInnerExecNode interface {
	GetName() string
	SetExec(exec IExecNode)
	IsInPortExec(index int) bool
	IsOutPortExec(index int) bool
	GetInPortCount() int
	GetOutPortCount() int
	CloneInOutPort() ([]IPort, []IPort)

	GetInPort(index int) IPort
	GetOutPort(index int) IPort

	GetOutPortParamStartIndex() int
}

// IBaseExecNode 实际注册的执行结点的基础结构体
type IBaseExecNode interface {
	initInnerExecNode(innerNode *innerExecNode)
	initExecNode(gr *Graph, en *execNode) error
	GetPorts() ([]IPort, []IPort)
	getExecNodeInfo() (*ExecContext, *execNode)
	setExecNodeInfo(gr *ExecContext, en *execNode)
	GetBlueprintModule() IBlueprintModule
}

// IExecNode 实际注册的执行结点
type IExecNode interface {
	IBaseExecNode
	GetName() string
	DoNext(index int) error
	Exec() (int, error) // 返回后续执行的Node的Index
	GetNextExecLen() int
	getInnerExecNode() IInnerExecNode
}

// 配置对应的基础信息+端口数据
type innerExecNode struct {
	Name        string
	Title       string
	Package     string
	Description string

	inPort  []IPort // 下标即为portId
	outPort []IPort // 下标即为portId

	outPortParamStartIndex int // 输出参数的起始索引,用于排除执行出口

	IExecNode // 实际注册的执行结点
}

type BaseExecNode struct {
	*innerExecNode // 内部注册的执行结点

	// 执行时初始化的数据
	*ExecContext
	gr       *Graph
	execNode *execNode
}

type InputConfig struct {
	Name      string `json:"name"`
	PortType  string `json:"type"`
	DataType  string `json:"data_type"`
	HasInput  bool   `json:"has_input"`
	PinWidget string `json:"pin_widget"`
	PortId    int    `json:"port_id"`
}

type OutputConfig struct {
	Name     string `json:"name"`
	PortType string `json:"type"`
	DataType string `json:"data_type"`
	HasInput bool   `json:"has_input"`
	PortId   int    `json:"port_id"`
}

type BaseExecConfig struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Package     string         `json:"package"`
	Description string         `json:"description"`
	IsPure      bool           `json:"is_pure"`
	Inputs      []InputConfig  `json:"inputs"`
	Outputs     []OutputConfig `json:"outputs"`
}

func (bc *BaseExecConfig) GetMaxInPortId() int {
	maxPortId := -1
	for i := range bc.Inputs {
		if bc.Inputs[i].PortId > maxPortId {
			maxPortId = bc.Inputs[i].PortId
		}
	}

	return maxPortId
}

func (bc *BaseExecConfig) GetMaxOutPortId() int {
	maxPortId := -1
	for i := range bc.Outputs {
		if bc.Outputs[i].PortId > maxPortId {
			maxPortId = bc.Outputs[i].PortId
		}
	}

	return maxPortId
}

func (em *innerExecNode) PrepareMaxInPortId(maxInPortId int) {
	em.inPort = make([]IPort, maxInPortId+1)
}

func (em *innerExecNode) PrepareMaxOutPortId(maxOutPortId int) {
	em.outPort = make([]IPort, maxOutPortId+1)
}

func (em *innerExecNode) SetInPortById(id int, port IPort) bool {
	if id < 0 || id >= len(em.inPort) {
		return false
	}
	em.inPort[id] = port
	return true
}

func (em *innerExecNode) SetOutPortById(id int, port IPort) bool {
	if id < 0 || id >= len(em.outPort) {
		return false
	}
	em.outPort[id] = port

	// 分析执行的
	em.outPortParamStartIndex = -1
	for i := range em.outPort {
		if em.outPort[i] == nil {
			continue
		}

		// 遇到非Exec结点，即为输出参数开始位置
		if !em.outPort[i].IsPortExec() {
			em.outPortParamStartIndex = i
			break
		}
	}

	return true
}

func (em *innerExecNode) GetOutPortParamStartIndex() int {
	return em.outPortParamStartIndex
}

func (em *innerExecNode) GetName() string {
	return em.Name
}

func (em *innerExecNode) SetExec(exec IExecNode) {
	em.IExecNode = exec
}

func (em *innerExecNode) CloneInOutPort() ([]IPort, []IPort) {
	inPorts := make([]IPort, 0, 2)
	for _, port := range em.inPort {
		if port == nil {
			inPorts = append(inPorts, nil)
			continue
		}

		if port.IsPortExec() {
			// 执行入口, 不需要克隆,占位处理
			inPorts = append(inPorts, nil)
			continue
		}

		inPorts = append(inPorts, port.Clone())
	}
	outPorts := make([]IPort, 0, 2)

	for _, port := range em.outPort {
		if port == nil {
			outPorts = append(outPorts, nil)
			continue
		}

		if port.IsPortExec() {
			outPorts = append(outPorts, nil)
			continue
		}
		outPorts = append(outPorts, port.Clone())
	}

	return inPorts, outPorts
}

func (em *innerExecNode) IsInPortExec(index int) bool {
	if index >= len(em.inPort) || index < 0 {
		return false
	}

	return em.inPort[index].IsPortExec()
}

func (em *innerExecNode) IsOutPortExec(index int) bool {
	if index >= len(em.outPort) || index < 0 {
		return false
	}

	return em.outPort[index].IsPortExec()
}

func (em *innerExecNode) GetInPortCount() int {
	return len(em.inPort)
}

func (em *innerExecNode) GetOutPortCount() int {
	return len(em.outPort)
}

func (em *innerExecNode) GetInPort(index int) IPort {
	if index >= len(em.inPort) || index < 0 {
		return nil
	}
	return em.inPort[index]
}

func (em *innerExecNode) GetOutPort(index int) IPort {
	if index >= len(em.outPort) || index < 0 {
		return nil
	}
	return em.outPort[index]
}

func (en *BaseExecNode) GetVariableName() string {
	return en.execNode.variableName
}

func (en *BaseExecNode) GetBluePrintModule() IBlueprintModule {
	return en.gr.IBlueprintModule
}

func (en *BaseExecNode) initInnerExecNode(innerNode *innerExecNode) {
	en.innerExecNode = innerNode
}

func (en *BaseExecNode) getExecNodeInfo() (*ExecContext, *execNode) {
	return en.ExecContext, en.execNode
}

func (en *BaseExecNode) setExecNodeInfo(c *ExecContext, e *execNode) {
	en.ExecContext = c
	en.execNode = e
}

func (en *BaseExecNode) initExecNode(gr *Graph, node *execNode) error {
	ctx, ok := gr.context[node.Id]
	if !ok {
		return fmt.Errorf("node %s not found", node.Id)
	}

	en.ExecContext = ctx
	en.gr = gr
	en.execNode = node
	return nil
}

func (en *BaseExecNode) GetPorts() ([]IPort, []IPort) {
	return en.InputPorts, en.OutputPorts
}

func (en *BaseExecNode) GetInPort(index int) IPort {
	if en.InputPorts == nil {
		return nil
	}

	if index >= len(en.InputPorts) || index < 0 {
		return nil
	}
	return en.InputPorts[index]
}

func (en *BaseExecNode) GetOutPort(index int) IPort {
	if en.OutputPorts == nil {
		return nil
	}
	if index >= len(en.OutputPorts) || index < 0 {
		return nil
	}
	return en.OutputPorts[index]
}

func (en *BaseExecNode) SetOutPort(index int, val IPort) bool {
	if index >= len(en.OutputPorts) || index < 0 {
		return false
	}
	en.OutputPorts[index].SetValue(val)
	return true
}

func (en *BaseExecNode) GetInPortInt(index int) (Port_Int, bool) {
	port := en.GetInPort(index)
	if port == nil {
		return 0, false
	}

	return port.GetInt()
}

func (en *BaseExecNode) GetInPortFloat(index int) (Port_Float, bool) {
	port := en.GetInPort(index)
	if port == nil {
		return 0, false
	}
	return port.GetFloat()
}

func (en *BaseExecNode) GetInPortStr(index int) (Port_Str, bool) {
	port := en.GetInPort(index)
	if port == nil {
		return "", false
	}
	return port.GetStr()
}

func (en *BaseExecNode) GetInPortArray(index int) (Port_Array, bool) {
	port := en.GetInPort(index)
	if port == nil {
		return nil, false
	}
	return port.GetArray()
}

func (en *BaseExecNode) GetInPortArrayValInt(index int, idx int) (Port_Int, bool) {
	port := en.GetInPort(index)
	if port == nil {
		return 0, false
	}
	return port.GetArrayValInt(idx)
}

func (en *BaseExecNode) GetInPortArrayValStr(idx int) (Port_Str, bool) {
	port := en.GetInPort(idx)
	if port == nil {
		return "", false
	}
	return port.GetArrayValStr(idx)
}

func (en *BaseExecNode) GetInPortBool(index int) (Port_Bool, bool) {
	port := en.GetInPort(index)
	if port == nil {
		return false, false
	}
	return port.GetBool()
}

func (en *BaseExecNode) GetOutPortInt(index int) (Port_Int, bool) {
	port := en.GetOutPort(index)
	if port == nil {
		return 0, false
	}
	return port.GetInt()
}

func (en *BaseExecNode) GetOutPortFloat(index int) (Port_Float, bool) {
	port := en.GetOutPort(index)
	if port == nil {
		return 0, false
	}
	return port.GetFloat()
}

func (en *BaseExecNode) GetOutPortStr(index int) (Port_Str, bool) {
	port := en.GetOutPort(index)
	if port == nil {
		return "", false
	}
	return port.GetStr()
}

func (en *BaseExecNode) GetOutPortArrayValInt(index int, idx int) (Port_Int, bool) {
	port := en.GetOutPort(index)
	if port == nil {
		return 0, false
	}
	return port.GetArrayValInt(idx)
}

func (en *BaseExecNode) GetOutPortArrayValStr(index int, idx int) (Port_Str, bool) {
	port := en.GetOutPort(index)
	if port == nil {
		return "", false
	}
	return port.GetArrayValStr(idx)
}

func (en *BaseExecNode) GetOutPortBool(index int) (Port_Bool, bool) {
	port := en.GetOutPort(index)
	if port == nil {
		return false, false
	}
	return port.GetBool()
}

func (en *BaseExecNode) SetInPortInt(index int, val Port_Int) bool {
	port := en.GetInPort(index)
	if port == nil {
		return false
	}
	return port.SetInt(val)
}

func (en *BaseExecNode) SetInPortFloat(index int, val Port_Float) bool {
	port := en.GetInPort(index)
	if port == nil {
		return false
	}
	return port.SetFloat(val)
}

func (en *BaseExecNode) SetInPortStr(index int, val Port_Str) bool {
	port := en.GetInPort(index)
	if port == nil {
		return false
	}
	return port.SetStr(val)
}

func (en *BaseExecNode) SetInBool(index int, val Port_Bool) bool {
	port := en.GetInPort(index)
	if port == nil {
		return false
	}
	return port.SetBool(val)
}

func (en *BaseExecNode) SetInPortArrayValInt(index int, idx int, val Port_Int) bool {
	port := en.GetInPort(index)
	if port == nil {
		return false
	}
	return port.SetArrayValInt(idx, val)
}

func (en *BaseExecNode) SetInPortArrayValStr(index int, idx int, val Port_Str) bool {
	port := en.GetInPort(index)
	if port == nil {
		return false
	}
	return port.SetArrayValStr(idx, val)
}

func (en *BaseExecNode) AppendInPortArrayValInt(index int, val Port_Int) bool {
	port := en.GetInPort(index)
	if port == nil {
		return false
	}
	return port.AppendArrayValInt(val)
}

func (en *BaseExecNode) AppendInPortArrayValStr(index int, val Port_Str) bool {
	port := en.GetInPort(index)
	if port == nil {
		return false
	}
	return port.AppendArrayValStr(val)
}

func (en *BaseExecNode) GetInPortArrayLen(index int) Port_Int {
	port := en.GetInPort(index)
	if port == nil {
		return 0
	}
	return port.GetArrayLen()
}

func (en *BaseExecNode) SetOutPortInt(index int, val Port_Int) bool {
	port := en.GetOutPort(index)
	if port == nil {
		return false
	}
	return port.SetInt(val)
}

func (en *BaseExecNode) SetOutPortFloat(index int, val Port_Float) bool {
	port := en.GetOutPort(index)
	if port == nil {
		return false
	}
	return port.SetFloat(val)
}

func (en *BaseExecNode) SetOutPortStr(index int, val Port_Str) bool {
	port := en.GetOutPort(index)
	if port == nil {
		return false
	}
	return port.SetStr(val)
}

func (en *BaseExecNode) SetOutPortBool(index int, val Port_Bool) bool {
	port := en.GetOutPort(index)
	if port == nil {
		return false
	}
	return port.SetBool(val)
}

func (en *BaseExecNode) SetOutPortArrayValInt(index int, idx int, val Port_Int) bool {
	port := en.GetOutPort(index)
	if port == nil {
		return false
	}
	return port.SetArrayValInt(idx, val)
}

func (en *BaseExecNode) SetOutPortArrayValStr(index int, idx int, val Port_Str) bool {
	port := en.GetOutPort(index)
	if port == nil {
		return false
	}
	return port.SetArrayValStr(idx, val)
}

func (en *BaseExecNode) AppendOutPortArrayValInt(index int, val Port_Int) bool {
	port := en.GetOutPort(index)
	if port == nil {
		return false
	}
	return port.AppendArrayValInt(val)
}

func (en *BaseExecNode) AppendOutPortArrayValStr(index int, val Port_Str) bool {
	port := en.GetOutPort(index)
	if port == nil {
		return false
	}
	return port.AppendArrayValStr(val)
}

func (en *BaseExecNode) GetOutPortArrayLen(index int) Port_Int {
	port := en.GetOutPort(index)
	if port == nil {
		return 0
	}
	return port.GetArrayLen()
}

func (en *BaseExecNode) DoNext(index int) error {
	// -1 表示中断运行
	if index == -1 {
		return nil
	}

	if index < 0 || index >= len(en.execNode.nextNode) {
		return fmt.Errorf("next index %d not found", index)
	}

	if en.execNode.nextNode[index] == nil {
		return nil
	}

	return en.execNode.nextNode[index].Do(en.gr)
}

func (en *BaseExecNode) GetNextExecLen() int {
	return len(en.execNode.nextNode)
}

func (en *BaseExecNode) getInnerExecNode() IInnerExecNode {
	return en.innerExecNode.IExecNode.(IInnerExecNode)
}

func (en *BaseExecNode) GetBlueprintModule() IBlueprintModule {
	if en.gr == nil {
		return nil
	}

	return en.gr.IBlueprintModule
}

func (en *BaseExecNode)  GetAndCreateReturnPort() IPort{
	if en.gr == nil {
		return nil
	}
	
	return en.gr.GetAndCreateReturnPort()
}