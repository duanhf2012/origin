package node

import (
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/v2/cluster"
	"github.com/duanhf2012/origin/v2/console"
	"github.com/duanhf2012/origin/v2/log"
	"github.com/duanhf2012/origin/v2/profiler"
	"github.com/duanhf2012/origin/v2/service"
	"github.com/duanhf2012/origin/v2/util/buildtime"
	"github.com/duanhf2012/origin/v2/util/timer"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	"github.com/duanhf2012/origin/v2/util/sysprocess"
)

var sig chan os.Signal
var nodeId int
var preSetupService []service.IService //预安装
var profilerInterval time.Duration
var bValid bool
var configDir = "./config/"

const(
	SingleStop   syscall.Signal = 10
	SignalRetire syscall.Signal = 12
)

type BuildOSType = int8

const(
	Windows BuildOSType = 0
	Linux 	BuildOSType = 1
	Mac   	BuildOSType = 2
)

func init() {
	sig = make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, SingleStop,SignalRetire)

	console.RegisterCommandBool("help", false, "<-help> This help.", usage)
	console.RegisterCommandString("name", "", "<-name nodeName> Node's name.", setName)
	console.RegisterCommandString("start", "", "<-start nodeid=nodeid> Run originserver.", startNode)
	console.RegisterCommandString("stop", "", "<-stop nodeid=nodeid> Stop originserver process.", stopNode)
	console.RegisterCommandString("retire", "", "<-retire nodeid=nodeid> retire originserver process.", retireNode)
	console.RegisterCommandString("config", "", "<-config path> Configuration file path.", setConfigPath)
	console.RegisterCommandString("console", "", "<-console true|false> Turn on or off screen log output.", openConsole)
	console.RegisterCommandString("loglevel", "debug", "<-loglevel debug|release|warning|error|fatal> Set loglevel.", setLevel)
	console.RegisterCommandString("logpath", "", "<-logpath path> Set log file path.", setLogPath)
	console.RegisterCommandInt("logsize", 0, "<-logsize size> Set log size(MB).", setLogSize)
	console.RegisterCommandInt("logchannelcap", 0, "<-logchannelcap num> Set log channel cap.", setLogChannelCapNum)
	console.RegisterCommandString("pprof", "", "<-pprof ip:port> Open performance analysis.", setPprof)
}


func notifyAllServiceRetire(){
	service.NotifyAllServiceRetire()
}

func usage(val interface{}) error {
	ret := val.(bool)
	if ret == false {
		return nil
	}

	if len(buildtime.GetBuildDateTime()) > 0 {
		fmt.Fprintf(os.Stderr, "Welcome to Origin(build info: %s)\nUsage: originserver [-help] [-start node=1] [-stop] [-config path] [-pprof 0.0.0.0:6060]...\n", buildtime.GetBuildDateTime())
	} else {
		fmt.Fprintf(os.Stderr, "Welcome to Origin\nUsage: originserver [-help] [-start node=1] [-stop] [-config path] [-pprof 0.0.0.0:6060]...\n")
	}

	console.PrintDefaults()
	return nil
}

func setName(val interface{}) error {
	return nil
}

func setPprof(val interface{}) error {
	listenAddr := val.(string)
	if listenAddr == "" {
		return nil
	}

	go func() {
		err := http.ListenAndServe(listenAddr, nil)
		if err != nil {
			panic(fmt.Errorf("%+v", err))
		}
	}()

	return nil
}

func setConfigPath(val interface{}) error {
	configPath := val.(string)
	if configPath == "" {
		return nil
	}
	_, err := os.Stat(configPath)
	if err != nil {
		return fmt.Errorf("Cannot find file path %s", configPath)
	}

	cluster.SetConfigDir(configPath)
	configDir = configPath
	return nil
}

func getRunProcessPid(nodeId int) (int, error) {
	f, err := os.OpenFile(fmt.Sprintf("%s_%d.pid", os.Args[0], nodeId), os.O_RDONLY, 0600)
	defer f.Close()
	if err != nil {
		return 0, err
	}

	pidByte, errs := io.ReadAll(f)
	if errs != nil {
		return 0, errs
	}

	return strconv.Atoi(string(pidByte))
}

func writeProcessPid(nodeId int) {
	//pid
	f, err := os.OpenFile(fmt.Sprintf("%s_%d.pid", os.Args[0], nodeId), os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
	defer f.Close()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(-1)
	} else {
		_, err = f.Write([]byte(fmt.Sprintf("%d", os.Getpid())))
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(-1)
		}
	}
}

func GetNodeId() int {
	return nodeId
}

func initNode(id int) {
	//1.初始化集群
	nodeId = id
	err := cluster.GetCluster().Init(GetNodeId(), Setup)
	if err != nil {
		log.Fatal("read system config is error ",log.ErrorAttr("error",err))
	}

	err = initLog()
	if err != nil {
		return
	}

	//2.顺序安装服务
	serviceOrder := cluster.GetCluster().GetLocalNodeInfo().ServiceList
	for _,serviceName:= range serviceOrder{
		bSetup := false
		for _, s := range preSetupService {
			if s.GetName() != serviceName {
				continue
			}
			bSetup = true
			pServiceCfg := cluster.GetCluster().GetServiceCfg(s.GetName())
			s.Init(s, cluster.GetRpcClient, cluster.GetRpcServer, pServiceCfg)

			service.Setup(s)
		}

		if bSetup == false {
			log.Fatal("Service name "+serviceName+" configuration error")
		}
	}

	//3.service初始化
	service.Init()
}

func initLog() error {
	if log.LogPath == "" {
		setLogPath("./log")
	}

	localnodeinfo := cluster.GetCluster().GetLocalNodeInfo()
	filepre := fmt.Sprintf("%s_%d_", localnodeinfo.NodeName, localnodeinfo.NodeId)
	logger, err := log.NewTextLogger(log.LogLevel,log.LogPath,filepre,true,log.LogChannelCap)
	if err != nil {
		fmt.Printf("cannot create log file!\n")
		return err
	}
	log.Export(logger)
	return nil
}

func Start() {
	err := console.Run(os.Args)
	if err != nil {
		fmt.Printf("%+v\n", err)
		return
	}
}


func retireNode(args interface{}) error {
	//1.解析参数
	param := args.(string)
	if param == "" {
		return nil
	}

	sParam := strings.Split(param, "=")
	if len(sParam) != 2 {
		return fmt.Errorf("invalid option %s", param)
	}
	if sParam[0] != "nodeid" {
		return fmt.Errorf("invalid option %s", param)
	}
	nId, err := strconv.Atoi(sParam[1])
	if err != nil {
		return fmt.Errorf("invalid option %s", param)
	}

	processId, err := getRunProcessPid(nId)
	if err != nil {
		return err
	}


	RetireProcess(processId)
	return nil
}


func stopNode(args interface{}) error {
	//1.解析参数
	param := args.(string)
	if param == "" {
		return nil
	}

	sParam := strings.Split(param, "=")
	if len(sParam) != 2 {
		return fmt.Errorf("invalid option %s", param)
	}
	if sParam[0] != "nodeid" {
		return fmt.Errorf("invalid option %s", param)
	}
	nId, err := strconv.Atoi(sParam[1])
	if err != nil {
		return fmt.Errorf("invalid option %s", param)
	}

	processId, err := getRunProcessPid(nId)
	if err != nil {
		return err
	}

	KillProcess(processId)
	return nil
}

func startNode(args interface{}) error {
	//1.解析参数
	param := args.(string)
	if param == "" {
		return nil
	}

	sParam := strings.Split(param, "=")
	if len(sParam) != 2 {
		return fmt.Errorf("invalid option %s", param)
	}
	if sParam[0] != "nodeid" {
		return fmt.Errorf("invalid option %s", param)
	}
	nodeId, err := strconv.Atoi(sParam[1])
	if err != nil {
		return fmt.Errorf("invalid option %s", param)
	}
	for{
		processId, pErr := getRunProcessPid(nodeId)
		if pErr != nil {
			break
		}

		name, cErr := sysprocess.GetProcessNameByPID(int32(processId))
		myName, mErr := sysprocess.GetMyProcessName()
		//当前进程名获取失败，不应该发生
		if mErr != nil {
			log.SInfo("get my process's name is error,", err.Error())
			os.Exit(-1)
		}

		//进程id存在，而且进程名也相同，被认为是当前进程重复运行
		if cErr == nil && name == myName {
			log.SInfo(fmt.Sprintf("repeat runs are not allowed,node is %d,processid is %d",nodeId,processId))
			os.Exit(-1)
		}
		break
	}

	//2.记录进程id号
	log.Info("Start running server.")
	writeProcessPid(nodeId)
	timer.StartTimer(10*time.Millisecond, 1000000)

	//3.初始化node
	initNode(nodeId)

	//4.运行service
	service.Start()

	//5.运行集群
	cluster.GetCluster().Start()



	//6.监听程序退出信号&性能报告
	bRun := true
	var pProfilerTicker *time.Ticker = &time.Ticker{}
	if profilerInterval > 0 {
		pProfilerTicker = time.NewTicker(profilerInterval)
	}

	for bRun {
		select {
		case s := <-sig:
			signal := s.(syscall.Signal)
			if signal == SignalRetire {
				log.Info("receipt retire signal.")
				notifyAllServiceRetire()
			}else {
				bRun = false
				log.Info("receipt stop signal.")
			}
		case <-pProfilerTicker.C:
			profiler.Report()
		}
	}

	cluster.GetCluster().Stop()
	//7.退出
	service.StopAllService()

	log.Info("Server is stop.")
	log.Close()
	return nil
}

func Setup(s ...service.IService) {
	for _, sv := range s {
		sv.OnSetup(sv)
		preSetupService = append(preSetupService, sv)
	}
}

func GetService(serviceName string) service.IService {
	return service.GetService(serviceName)
}

func SetConfigDir(cfgDir string) {
	configDir = cfgDir
	cluster.SetConfigDir(cfgDir)
}

func GetConfigDir() string {
	return configDir
}

func OpenProfilerReport(interval time.Duration) {
	profilerInterval = interval
}

func openConsole(args interface{}) error {
	if args == "" {
		return nil
	}
	strOpen := strings.ToLower(strings.TrimSpace(args.(string)))
	if strOpen == "false" {
		log.OpenConsole = false
	} else if strOpen == "true" {
		log.OpenConsole = true
	} else {
		return errors.New("Parameter console error!")
	}
	return nil
}

func setLevel(args interface{}) error {
	if args == "" {
		return nil
	}

	strlogLevel := strings.TrimSpace(args.(string))
	switch strlogLevel {
	case "trace":
		log.LogLevel = log.LevelTrace
	case "debug":
		log.LogLevel = log.LevelDebug
	case "info":
		log.LogLevel = log.LevelInfo
	case "warning":
		log.LogLevel = log.LevelWarning
	case "error":
		log.LogLevel = log.LevelError
	case "stack":
		log.LogLevel = log.LevelStack
	case "fatal":
		log.LogLevel = log.LevelFatal
	default:
		return errors.New("unknown level: " + strlogLevel)
	}
	return nil
}

func setLogPath(args interface{}) error {
	if args == "" {
		return nil
	}

	log.LogPath = strings.TrimSpace(args.(string))
	dir, err := os.Stat(log.LogPath) //这个文件夹不存在
	if err == nil && dir.IsDir() == false {
		return errors.New("Not found dir " + log.LogPath)
	}

	if err != nil {
		err = os.Mkdir(log.LogPath, os.ModePerm)
		if err != nil {
			return errors.New("Cannot create dir " + log.LogPath)
		}
	}

	return nil
}

func setLogSize(args interface{}) error {
	if args == "" {
		return nil
	}

	logSize,ok := args.(int)
	if ok == false{
		return errors.New("param logsize is error")
	}

	log.LogSize = int64(logSize)*1024*1024

	return nil
}

func setLogChannelCapNum(args interface{}) error {
	if args == "" {
		return nil
	}

	logChannelCap,ok := args.(int)
	if ok == false{
		return errors.New("param logsize is error")
	}

	log.LogChannelCap = logChannelCap
	return nil
}
