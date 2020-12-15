package node

import (
	"fmt"
	"github.com/duanhf2012/origin/cluster"
	"github.com/duanhf2012/origin/console"
	"github.com/duanhf2012/origin/log"
	"github.com/duanhf2012/origin/profiler"
	"github.com/duanhf2012/origin/service"
	"github.com/duanhf2012/origin/util/timer"
	"io/ioutil"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var closeSig chan bool
var sig chan os.Signal
var nodeId int
var preSetupService []service.IService //预安装
var profilerInterval time.Duration
var bValid bool
var configDir = "./config/"

func init() {

	closeSig = make(chan bool,1)
	sig = make(chan os.Signal, 3)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM,syscall.Signal(10))

	console.RegisterCommandBool("help",false,"This help.",usage)
	console.RegisterCommandString("start","","Run originserver.",startNode)
	console.RegisterCommandBool("stop",false,"Stop originserver process",stopNode)
	console.RegisterCommandString("config","","Configuration file path.",setConfigPath)
	console.RegisterCommandString("pprof","","Open performance analysis.",setPprof)
}

func usage(val interface{}) error{
	ret := val.(bool)
	if ret == false {
		return nil
	}

	fmt.Fprintf(os.Stderr, `orgin version: orgin/2.14.20201029
Usage: originserver [-help] [-start node=1] [-stop] [-config path] [-pprof 0.0.0.0:6060]
`)
	console.PrintDefaults()
	return nil
}

func setPprof(val interface{}) error {
	listenAddr := val.(string)
	if listenAddr==""{
		return nil
	}

	go func(){
		err := http.ListenAndServe(listenAddr, nil)
		if err != nil {
			panic(fmt.Errorf("%+v",err))
		}
	}()

	return nil
}

func setConfigPath(val interface{}) error{
	configPath := val.(string)
	if configPath==""{
		return nil
	}
	_, err := os.Stat(configPath)
	if err != nil {
		return fmt.Errorf("Cannot find file path %s",configPath)
	}

	cluster.SetConfigDir(configPath)
	configDir = configPath
	return nil
}

func  getRunProcessPid() (int,error) {
	f, err := os.OpenFile(os.Args[0]+".pid", os.O_RDONLY, 0600)
	defer f.Close()
	if err!= nil {
		return 0,err
	}

	pidByte,errs := ioutil.ReadAll(f)
	if errs!=nil {
		return 0,errs
	}

	return strconv.Atoi(string(pidByte))
}

func writeProcessPid() {
	//pid
	f, err := os.OpenFile(os.Args[0]+".pid", os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
	defer f.Close()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(-1)
	} else {
		_,err=f.Write([]byte(fmt.Sprintf("%d",os.Getpid())))
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(-1)
		}
	}
}

func GetNodeId() int {
	return nodeId
}

func initNode(id int){
	//1.初始化集群
	nodeId = id
	err := cluster.GetCluster().Init(GetNodeId())
	if err != nil {
		log.Fatal("read system config is error %+v",err)
	}

	//2.setup service
	for _,s := range preSetupService {
		//是否配置的service
		if cluster.GetCluster().IsConfigService(s.GetName()) == false {
			continue
		}

		pServiceCfg := cluster.GetCluster().GetServiceCfg(s.GetName())
		s.Init(s,cluster.GetRpcClient,cluster.GetRpcServer,pServiceCfg)

		service.Setup(s)
	}

	//3.service初始化
	service.Init(closeSig)
}

func Start() {
	err := console.Run(os.Args)
	if err!=nil {
		fmt.Printf("%+v\n",err)
		return
	}
}

func stopNode(args interface{}) error {
	isStop := args.(bool)
	if isStop == false{
		return nil
	}

	processId,err := getRunProcessPid()
	if err != nil {
		return err
	}

	KillProcess(processId)
	return nil
}

func startNode(args interface{}) error{
	//1.解析参数
	param := args.(string)
	if param == "" {
		return nil
	}

	sParam := strings.Split(param,"=")
	if len(sParam) != 2 {
		return fmt.Errorf("invalid option %s",param)
	}
	if sParam[0]!="nodeid" {
		return fmt.Errorf("invalid option %s",param)
	}
	nodeId,err:= strconv.Atoi(sParam[1])
	if err != nil {
		return fmt.Errorf("invalid option %s",param)
	}

	timer.StartTimer(10*time.Millisecond,100000)
	log.Release("Start running server.")
	//2.初始化node
	initNode(nodeId)

	//3.运行集群
	cluster.GetCluster().Start()

	//4.运行service
	service.Start()

	//5.记录进程id号
	writeProcessPid()

	//6.监听程序退出信号&性能报告
	bRun := true
	var pProfilerTicker *time.Ticker = &time.Ticker{}
	if profilerInterval>0 {
		pProfilerTicker = time.NewTicker(profilerInterval)
	}
	for bRun {
		select {
		case <-sig:
			log.Debug("receipt stop signal.")
			bRun = false
		case <- pProfilerTicker.C:
			profiler.Report()
		}
	}
	cluster.GetCluster().Stop()
	//7.退出
	close(closeSig)
	service.WaitStop()

	log.Debug("Server is stop.")
	return nil
}


func Setup(s ...service.IService)  {
	for _,sv := range s {
		sv.OnSetup(sv)
		preSetupService = append(preSetupService,sv)
	}
}

func GetService(serviceName string) service.IService {
	return service.GetService(serviceName)
}

func SetConfigDir(configDir string){
	configDir = configDir
	cluster.SetConfigDir(configDir)
}

func GetConfigDir() string {
	return configDir
}

func SetSysLog(strLevel string, pathname string, flag int){
	logs,_:= log.New(strLevel,pathname,flag)
	log.Export(logs)
}

func OpenProfilerReport(interval time.Duration){
	profilerInterval = interval
}

