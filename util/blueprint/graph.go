package blueprint

import (
	"fmt"
	"time"

	"github.com/duanhf2012/origin/v2/log"

	"github.com/goccy/go-json"
)

const ReturnVarial = "g_Return"

var IsDebug = false

type IGraph interface {
	Do(entranceID int64, args ...any) (Port_Array, error)
	Release()
	GetGraphFileName() string
	HotReload(newBaseGraph *baseGraph)
	Clone() IGraph
}

type IBlueprintModule interface {
	SafeAfterFunc(timerId *uint64, d time.Duration, AdditionData interface{}, cb func(uint64, interface{}))
	TriggerEvent(graphID int64, eventID int64, args ...any) error
	CancelTimerId(graphID int64, timerId *uint64) bool
}

type baseGraph struct {
	entrance map[int64]*execNode // 入口
}

type Graph struct {
	graphFileName string
	graphID       int64
	*baseGraph
	graphContext
	IBlueprintModule
	mapTimerID map[uint64]struct{}
}

type graphContext struct {
	context         map[string]*ExecContext // 上下文
	variables       map[string]IPort        // 变量
	globalVariables map[string]IPort        // 全局变量,g_Return,为执行返回值
}

type nodeConfig struct {
	Id     string `json:"id"`
	Class  string `json:"class"`
	Module string `json:"module"`
	//Pos         []float64              `json:"pos"`
	PortDefault map[string]interface{} `json:"port_defaultv"`
}

type edgeConfig struct {
	EdgeID       string `json:"edge_id"`
	SourceNodeID string `json:"source_node_id"`
	DesNodeId    string `json:"des_node_id"`

	SourcePortId int `json:"source_port_id"`
	DesPortId    int `json:"des_port_id"`
}

type MultiTypeValue struct {
	Value any
}

// 实现json.Unmarshaler接口，自定义解码逻辑
func (v *MultiTypeValue) UnmarshalJSON(data []byte) error {
	// 尝试将数据解析为字符串
	var strVal string
	if err := json.Unmarshal(data, &strVal); err == nil {
		v.Value = strVal
		return nil
	}

	// 如果不是字符串，尝试解析为数字
	var intVal int
	if err := json.Unmarshal(data, &intVal); err == nil {
		v.Value = intVal
		return nil
	}

	// 如果不是字符串，尝试解析为数字
	var boolVal bool
	if err := json.Unmarshal(data, &boolVal); err == nil {
		v.Value = boolVal
		return nil
	}

	// 如果不是字符串，尝试解析为数字
	var float64Val float64
	if err := json.Unmarshal(data, &float64Val); err == nil {
		v.Value = float64Val
		return nil
	}

	var arrayVal []any
	if err := json.Unmarshal(data, &arrayVal); err == nil {
		v.Value = arrayVal
		return nil
	}
	// 如果都失败，返回错误
	return fmt.Errorf("cannot unmarshal JSON value: %s", string(data))
}

type variablesConfig struct {
	Name  string         `json:"name"`
	Type  string         `json:"type"`
	Value MultiTypeValue `json:"value"`
}

type graphConfig struct {
	GraphName string `json:"graph_name"`
	Time      string `json:"time"`

	Nodes     []nodeConfig      `json:"nodes"`
	Edges     []edgeConfig      `json:"edges"`
	Variables []variablesConfig `json:"variables"`
}

func (gc *graphConfig) GetVariablesByName(varName string) *variablesConfig {
	for _, varCfg := range gc.Variables {
		if varCfg.Name == varName {
			return &varCfg
		}
	}

	return nil
}

func (gc *graphConfig) GetNodeByID(nodeID string) *nodeConfig {
	for _, node := range gc.Nodes {
		if node.Id == nodeID {
			return &node
		}
	}

	return nil
}

func (gr *Graph) GetAndCreateReturnPort() IPort {
	p, ok := gr.globalVariables[ReturnVarial]
	if ok && p != nil {
		return p
	}

	p = NewPortArray()
	gr.globalVariables[ReturnVarial] = p
	return p
}

func (gr *Graph) Do(entranceID int64, args ...any) (Port_Array, error) {
	if IsDebug {
		log.Debug("Graph Do", log.String("graphName", gr.graphFileName), log.Int64("graphID", gr.graphID), log.Int64("entranceID", entranceID))
	}

	entranceNode := gr.entrance[entranceID]
	if entranceNode == nil {
		return nil, fmt.Errorf("entranceID:%d not found", entranceID)
	}

	gr.variables = map[string]IPort{}
	gr.context = map[string]*ExecContext{}

	if gr.globalVariables == nil {
		gr.globalVariables = map[string]IPort{}
	} else {
		gr.globalVariables[ReturnVarial] = nil
	}

	err := entranceNode.Do(gr, args...)
	if err != nil {
		return nil, err
	}

	if gr.globalVariables != nil {
		port := gr.globalVariables[ReturnVarial]
		if port != nil {
			array, ok := port.GetArray()
			if ok {
				return array, nil
			}
		}
	}

	return nil, nil
}

func (gr *Graph) GetNodeInPortValue(nodeID string, inPortIndex int) IPort {
	if ctx, ok := gr.context[nodeID]; ok {
		if inPortIndex >= len(ctx.InputPorts) || inPortIndex < 0 {
			return nil
		}

		return ctx.InputPorts[inPortIndex]
	}
	return nil
}

func (gr *Graph) GetNodeOutPortValue(nodeID string, outPortIndex int) IPort {
	if ctx, ok := gr.context[nodeID]; ok {
		if outPortIndex >= len(ctx.OutputPorts) || outPortIndex < 0 {
			return nil
		}
		return ctx.OutputPorts[outPortIndex]
	}
	return nil
}

func (gr *Graph) Release() {
	// 有定时器关闭定时器
	for timerID := range gr.mapTimerID {
		gr.CancelTimerId(gr.graphID, &timerID)
	}
	gr.mapTimerID = nil

	// 清理掉所有数据
	*gr = Graph{}
}

func (gr *Graph) HotReload(newBaseGraph *baseGraph) {
	gr.baseGraph = newBaseGraph
}

func (gr *Graph) GetGraphFileName() string {
	return gr.graphFileName
}

func (gr *Graph) Clone() IGraph {
	cloneGr := *gr
	return &cloneGr
}
