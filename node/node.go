package node

import (
	"errors"
	"fmt"
	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/console"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/profiler"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/util/buildtime"
	"github.com/duanhf2012/origin/util/timer"
	"io"
	slog "log"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var sig chan os.Signal
var nodeId int
var preSetupService []service.IService //预安装
var profilerInterval time.Duration
var bValid bool
var configDir = "./config/"
var logLevel string = "debug"
var logPath string
type BuildOSType = int8

const(
	Windows BuildOSType = 0
	Linux 	BuildOSType = 1
	Mac   	BuildOSType = 2
)

func init() {
	sig = make(chan os.Signal, 3)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.Signal(10))

	console.RegisterCommandBool("help", false, "<-help> This help.", usage)
	console.RegisterCommandString("name", "", "<-name nodeName> Node's name.", setName)
	console.RegisterCommandString("start", "", "<-start nodeid=nodeid> Run originserver.", startNode)
	console.RegisterCommandString("stop", "", "<-stop nodeid=nodeid> Stop originserver process.", stopNode)
	console.RegisterCommandString("config", "", "<-config path> Configuration file path.", setConfigPath)
	console.RegisterCommandString("console", "", "<-console true|false> Turn on or off screen log output.", openConsole)
	console.RegisterCommandString("loglevel", "debug", "<-loglevel debug|release|warning|error|fatal> Set loglevel.", setLevel)
	console.RegisterCommandString("logpath", "", "<-logpath path> Set log file path.", setLogPath)
	console.RegisterCommandString("pprof", "", "<-pprof ip:port> Open performance analysis.", setPprof)
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
		log.SFatal("read system config is error ", err.Error())
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
			log.SFatal("Service name "+serviceName+" configuration error")
		}
	}

	//3.service初始化
	service.Init()
}

func initLog() error {
	if logPath == "" {
		setLogPath("./log")
	}

	localnodeinfo := cluster.GetCluster().GetLocalNodeInfo()
	filepre := fmt.Sprintf("%s_%d_", localnodeinfo.NodeName, localnodeinfo.NodeId)
	logger, err := log.New(logLevel, logPath, filepre, slog.LstdFlags|slog.Lshortfile, 10)
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
	nodeId, err := strconv.Atoi(sParam[1])
	if err != nil {
		return fmt.Errorf("invalid option %s", param)
	}

	processId, err := getRunProcessPid(nodeId)
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

	timer.StartTimer(10*time.Millisecond, 1000000)
	log.SRelease("Start running server.")
	//2.初始化node
	initNode(nodeId)

	//3.运行service
	service.Start()

	//4.运行集群
	cluster.GetCluster().Start()

	//5.记录进程id号
	writeProcessPid(nodeId)

	//6.监听程序退出信号&性能报告
	bRun := true
	var pProfilerTicker *time.Ticker = &time.Ticker{}
	if profilerInterval > 0 {
		pProfilerTicker = time.NewTicker(profilerInterval)
	}
	for bRun {
		select {
		case <-sig:
			log.SRelease("receipt stop signal.")
			bRun = false
		case <-pProfilerTicker.C:
			profiler.Report()
		}
	}
	cluster.GetCluster().Stop()
	//7.退出
	service.StopAllService()

	log.SRelease("Server is stop.")
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

func SetSysLog(strLevel string, pathname string, flag int) {
	logs, _ := log.New(strLevel, pathname, "", flag, 10)
	log.Export(logs)
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

	logLevel = strings.TrimSpace(args.(string))
	if logLevel != "debug" && logLevel != "release" && logLevel != "warning" && logLevel != "error" && logLevel != "fatal" {
		return errors.New("unknown level: " + logLevel)
	}
	return nil
}

func setLogPath(args interface{}) error {
	if args == "" {
		return nil
	}
	logPath = strings.TrimSpace(args.(string))
	dir, err := os.Stat(logPath) //这个文件夹不存在
	if err == nil && dir.IsDir() == false {
		return errors.New("Not found dir " + logPath)
	}

	if err != nil {
		err = os.Mkdir(logPath, os.ModePerm)
		if err != nil {
			return errors.New("Cannot create dir " + logPath)
		}
	}

	return nil
}
