package blueprint

import (
	"fmt"
	"github.com/duanhf2012/origin/v2/log"
)

type prePortNode struct {
	node      *execNode // 上个结点
	outPortId int       // 对应上一个结点的OutPortId
}

type execNode struct {
	Id       string
	execNode IInnerExecNode

	nextNode []*execNode
	nextIdx  int

	preInPort          []*prePortNode // Port的上一个结点
	inPortDefaultValue map[string]any

	variableName string // 如果是变量，则有变量名
	beConnect    bool   // 是否有被连线
	isEntrance   bool   // 是否是入口结点
}

// HasBeConnectLine 是否有被连线
func (en *execNode) HasBeConnectLine() bool {
	return en.beConnect
}

// HasInPortExec 有前置执行入口
func (en *execNode) HasInPortExec() bool {
	return en.execNode.IsInPortExec(0)
}

// HasOutPortExec 有前置执行入口
func (en *execNode) HasOutPortExec() bool {
	return en.execNode.IsOutPortExec(0)
}

func (en *execNode) GetInPortDefaultValue(index int) any {
	key := fmt.Sprintf("%d", index)
	v, ok := en.inPortDefaultValue[key]
	if !ok {
		return nil
	}
	return v
}

func (en *execNode) GetInPortDefaultIntArrayValue(index int) []int64 {
	val := en.GetInPortDefaultValue(index)
	if val == nil {
		return nil
	}

	var arrayInt []int64
	arrayVal := val.([]any)
	for i := range arrayVal {
		if intVal, ok := arrayVal[i].(float64); ok {
			arrayInt = append(arrayInt, int64(intVal))
		}
	}
	return arrayInt
}

func (en *execNode) GetInPortDefaultStringArrayValue(index int) []string {
	val := en.GetInPortDefaultValue(index)
	if val == nil {
		return nil
	}

	return val.([]string)
}

func (en *execNode) Next() *execNode {
	if en.nextIdx >= len(en.nextNode) {
		return nil
	}

	return en.nextNode[en.nextIdx]
}

func (en *execNode) exec(gr *Graph) (int, error) {
	e, ok := en.execNode.(IExecNode)
	if !ok {
		return -1, fmt.Errorf("exec node %s not exec", en.execNode.GetName())
	}

	node, ok := en.execNode.(IBaseExecNode)
	if !ok {
		return -1, fmt.Errorf("exec node %s not exec", en.execNode.GetName())
	}

	// 执行完，要恢复上下文，结点的BaseExecNode会被执行时修改
	ctx, exNode := node.getExecNodeInfo()
	defer func() {
		node.setExecNodeInfo(ctx, exNode)
	}()

	if err := node.initExecNode(gr, en); err != nil {
		return -1, err
	}

	return e.Exec()
}

func (en *execNode) doSetInPort(gr *Graph, index int, inPort IPort) error {
	// 找到当前Node的InPort的index的前一个结点
	preNode := en.preInPort[index]
	// 如果前一个结点为空，则填充默认值
	if preNode == nil {
		err := inPort.setAnyVale(en.GetInPortDefaultValue(index))
		if err != nil {
			return err
		}
		return nil
	}

	if _, ok := gr.context[preNode.node.Id]; !ok ||
		(!preNode.node.HasBeConnectLine() && !preNode.node.isEntrance) {
		// 如果前一个结点没有执行过，则递归执行前一个结点
		err := preNode.node.Do(gr)
		if err != nil {
			return err
		}
	}

	// 判断上一个结点是否已经执行过
	if _, ok := gr.context[preNode.node.Id]; ok {
		outPort := gr.GetNodeOutPortValue(preNode.node.Id, preNode.outPortId)
		if outPort == nil {
			return fmt.Errorf("pre node %s out port index %d not found", preNode.node.Id, preNode.outPortId)
		}

		inPort.SetValue(outPort)
		return nil
	}

	return fmt.Errorf("pre node %s not exec", preNode.node.Id)
}

func (en *execNode) Do(gr *Graph, outPortArgs ...any) error {
	if IsDebug {
		log.Debug("Start ExecNode", log.String("Name", en.execNode.GetName()))
	}

	// 重新初始化上下文
	inPorts, outPorts := en.execNode.CloneInOutPort()
	gr.context[en.Id] = &ExecContext{
		InputPorts:  inPorts,
		OutputPorts: outPorts,
	}

	startOutIdx := en.execNode.GetOutPortParamStartIndex()
	for i := 0; i < len(outPortArgs); i++ {
		if i+startOutIdx >= len(outPorts) {
			return fmt.Errorf("args %d not found in node %s", i, en.execNode.GetName())
		}

		if err := outPorts[i+startOutIdx].setAnyVale(outPortArgs[i]); err != nil {
			return fmt.Errorf("args %d set value error: %w", i, err)
		}
	}

	// 处理InPort结点值
	var err error
	for portId := range inPorts {
		if en.execNode.IsInPortExec(portId) {
			continue
		}

		err = en.doSetInPort(gr, portId, inPorts[portId])
		if err != nil {
			return err
		}
	}

	// 设置执行器相关的上下文信息
	// 如果是变量设置变量名
	// 执行本结点
	nextIndex, execErr := en.exec(gr)

	// 调用logger记录执行日志
	if gr.logger != nil {
		gr.logger.LogNodeExec(en.execNode.GetName(), en.Id, inPorts, outPorts, nextIndex, execErr)
	}

	if execErr != nil {
		return execErr
	}

	if IsDebug {
		log.Debug("End ExecNode", log.String("Name", en.execNode.GetName()), log.Any("InPort", inPorts), log.Any("OutPort", outPorts))
	}

	if nextIndex == -1 || en.nextNode == nil {
		return nil
	}

	if nextIndex < 0 || nextIndex >= len(en.nextNode) {
		return fmt.Errorf("next index %d not found", nextIndex)
	}

	if en.nextNode[nextIndex] == nil {
		return nil
	}
	return en.nextNode[nextIndex].Do(gr)
}
