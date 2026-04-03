package blueprint

import (
	"fmt"
	"github.com/duanhf2012/origin/v2/log"
	"sync/atomic"
)

// IBlueprintLogger 蓝图执行日志接口
type IBlueprintLogger interface {
	// LogNodeExec 记录结点执行日志
	// nodeName: 结点名称
	// nodeID: 结点ID
	// inPorts: 输入端口列表
	// outPorts: 输出端口列表
	// execResult: 执行结果（-1表示中断，>=0表示后续执行的索引）
	// err: 执行错误
	LogNodeExec(nodeName string, nodeID string, inPorts []IPort, outPorts []IPort, execResult int, err error)
}

type Blueprint struct {
	execNodes    []IExecNode // 注册的定义执行结点
	execNodeList []func() IExecNode
	execPool     *ExecPool
	graphPool    *GraphPool

	blueprintModule IBlueprintModule
	mapGraph        map[int64]IGraph
	seedID          int64
	cancelTimer     func(*uint64) bool

	execDefFilePath string // 执行结点定义文件路径
	graphFilePath   string // 蓝图文件路径

	logger IBlueprintLogger // 蓝图执行日志记录器
}

func (bm *Blueprint) RegisterExecNode(execNodeFunc func() IExecNode) {
	bm.execNodeList = append(bm.execNodeList, execNodeFunc)
}

type IExecNodeType[T any] interface {
	*T
	IExecNode
}

// 生成一个泛型函数，返回func() IExecNode类型
func NewExecNode[T any, P IExecNodeType[T]]() func() IExecNode {
	return func() IExecNode {
		var t T
		return P(&t)
	}
}

func (bm *Blueprint) regSysNodes() {
	bm.RegisterExecNode(NewExecNode[AddInt]())
	bm.RegisterExecNode(NewExecNode[SubInt]())
	bm.RegisterExecNode(NewExecNode[MulInt]())
	bm.RegisterExecNode(NewExecNode[DivInt]())
	bm.RegisterExecNode(NewExecNode[ModInt]())
	bm.RegisterExecNode(NewExecNode[RandNumber]())

	bm.RegisterExecNode(NewExecNode[Entrance_ArrayParam]())
	bm.RegisterExecNode(NewExecNode[Entrance_IntParam]())
	bm.RegisterExecNode(NewExecNode[Entrance_Timer]())
	bm.RegisterExecNode(NewExecNode[DebugOutput]())
	bm.RegisterExecNode(NewExecNode[Sequence]())
	bm.RegisterExecNode(NewExecNode[Foreach]())
	bm.RegisterExecNode(NewExecNode[ForeachIntArray]())

	bm.RegisterExecNode(NewExecNode[GetArrayInt]())
	bm.RegisterExecNode(NewExecNode[GetArrayString]())
	bm.RegisterExecNode(NewExecNode[GetArrayLen]())
	bm.RegisterExecNode(NewExecNode[CreateIntArray]())
	bm.RegisterExecNode(NewExecNode[CreateStringArray]())
	bm.RegisterExecNode(NewExecNode[AppendIntegerToArray]())
	bm.RegisterExecNode(NewExecNode[AppendStringToArray]())
	bm.RegisterExecNode(NewExecNode[BoolIf]())
	bm.RegisterExecNode(NewExecNode[GreaterThanInteger]())
	bm.RegisterExecNode(NewExecNode[LessThanInteger]())
	bm.RegisterExecNode(NewExecNode[EqualInteger]())
	bm.RegisterExecNode(NewExecNode[RangeCompare]())
	bm.RegisterExecNode(NewExecNode[EqualSwitch]())
	bm.RegisterExecNode(NewExecNode[Probability]())
	bm.RegisterExecNode(NewExecNode[CreateTimer]())
	bm.RegisterExecNode(NewExecNode[AppendIntReturn]())
	bm.RegisterExecNode(NewExecNode[AppendStringReturn]())
	bm.RegisterExecNode(NewExecNode[IntInArray]())
}

func (bm *Blueprint) StartHotReload() (func(), error) {
	var execPool ExecPool
	var graphPool GraphPool

	// 加载配置结点生成名字对应的innerExecNode
	err := execPool.Load(bm.execDefFilePath)
	if err != nil {
		return nil, err
	}

	// 将注册的实际执行结点与innerExecNode进行关联
	for _, newExec := range bm.execNodeList {
		e := newExec()
		if !execPool.Register(e) {
			return nil, fmt.Errorf("register exec failed,exec:%s", e.GetName())
		}
	}

	// 加载所有的vgf蓝图文件
	err = graphPool.Load(&execPool, bm.graphFilePath, bm.blueprintModule)
	if err != nil {
		return nil, err
	}

	// 返回配置加载后的刷新内存处理
	return func() {
		// 替换旧的执行池和图池
		bm.execPool = &execPool
		bm.graphPool = &graphPool

		for _, gh := range bm.mapGraph {
			gFileName := gh.GetGraphFileName()
			bGraph := bm.graphPool.GetBaseGraph(gFileName)
			if bGraph == nil {
				log.Warn("GetBaseGraph fail", log.String("graph file name", gFileName))
				bGraph = &baseGraph{entrance: map[int64]*execNode{}}
			}

			gh.HotReload(bGraph)
		}
	}, nil

}

func (bm *Blueprint) Init(execDefFilePath string, graphFilePath string, blueprintModule IBlueprintModule, cancelTimer func(*uint64) bool, logger ...IBlueprintLogger) error {
	var execPool ExecPool
	var graphPool GraphPool

	// 加载配置结点生成名字对应的innerExecNode
	err := execPool.Load(execDefFilePath)
	if err != nil {
		return err
	}

	// 注册系统执行结点
	bm.regSysNodes()

	// 将注册的实际执行结点与innerExecNode进行关联
	for _, newExec := range bm.execNodeList {
		e := newExec()
		if !execPool.Register(e) {
			return fmt.Errorf("register exec failed,exec:%s", e.GetName())
		}
	}

	// 加载所有的vgf蓝图文件
	err = graphPool.Load(&execPool, graphFilePath, blueprintModule)
	if err != nil {
		return err
	}

	bm.execPool = &execPool
	bm.graphPool = &graphPool
	bm.cancelTimer = cancelTimer
	bm.blueprintModule = blueprintModule
	bm.mapGraph = make(map[int64]IGraph, 128)
	bm.execDefFilePath = execDefFilePath
	bm.graphFilePath = graphFilePath

	// 设置logger
	if len(logger) > 0 {
		bm.logger = logger[0]
	}

	return nil
}

func (bm *Blueprint) Create(graphName string) int64 {
	if graphName == "" {
		return 0
	}

	graphID := atomic.AddInt64(&bm.seedID, 1)
	gr := bm.graphPool.Create(graphName, graphID)
	if gr == nil {
		return 0
	}

	bm.mapGraph[graphID] = gr
	return graphID
}

func (bm *Blueprint) TriggerEvent(graphID int64, eventID int64, args ...any) error {
	graph := bm.mapGraph[graphID]
	if graph == nil {
		return fmt.Errorf("can not find graph:%d", graphID)
	}

	_, err := graph.Do(eventID, args...)
	return err
}

func (bm *Blueprint) Do(graphID int64, entranceID int64, args ...any) (Port_Array, error) {
	graph := bm.mapGraph[graphID]
	if graph == nil {
		return nil, nil
	}

	clone := graph.Clone()
	// 设置logger
	if g, ok := clone.(*Graph); ok {
		g.logger = bm.logger
	}
	return clone.Do(entranceID, args...)
}

func (bm *Blueprint) ReleaseGraph(graphID int64) {
	if graphID == 0 {
		return
	}

	defer delete(bm.mapGraph, graphID)
	graph := bm.mapGraph[graphID]
	if graph == nil {
		return
	}

	graph.Release()
}

func (bm *Blueprint) CancelTimerId(graphID int64, timerId *uint64) bool {
	tId := *timerId
	bm.cancelTimer(timerId)

	graph := bm.mapGraph[graphID]
	if graph == nil {
		return false
	}

	gr, ok := graph.(*Graph)
	if !ok {
		return false
	}

	delete(gr.mapTimerID, tId)
	return true
}

// GetLogger 获取logger
func (bm *Blueprint) GetLogger() IBlueprintLogger {
	return bm.logger
}

// GetGraphName 通过graphId获取蓝图名称
func (bm *Blueprint) GetGraphName(graphID int64) string {
	if graphID == 0 {
		return ""
	}

	graph := bm.mapGraph[graphID]
	if graph == nil {
		return ""
	}

	return graph.GetGraphFileName()
}
