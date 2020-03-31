package console

import (
	"fmt"
	"strconv"
	"strings"
)

var mapRegisterCmd map[string]func(param interface{}) =  map[string]func(param interface{}){}
var programName string

func Run(args []string) error {
	programName = args[0]
	if len(args) <= 1 {
		return fmt.Errorf("command not found, try `%s help` for help",args[0])
	}

	fn,ok := mapRegisterCmd[args[1]]
	if ok == false{
		return fmt.Errorf("command not found, try `%s help` for help",args[0])
	}

	switch  args[1] {
	case "start":
		if len(args)<2 {
			return fmt.Errorf("command not found, try `%s help` for help",args[0])
		}else{
			return start(fn,args[2])
		}
	}

	return fmt.Errorf("command not found, try `%s help` for help",args[0])
}

func start(fn func(param interface{}),param string) error {
	sparam := strings.Split(param,"=")
	if len(sparam) != 2 {
		return fmt.Errorf("invalid option %s",param)
	}
	if sparam[0]!="nodeid" {
		return fmt.Errorf("invalid option %s",param)
	}
	nodeId,err:= strconv.Atoi(sparam[1])
	if err != nil {
		return fmt.Errorf("invalid option %s",param)
	}

	fn(nodeId)
	return nil
}

func RegisterCommand(cmd string,fn func(param interface{})){
	mapRegisterCmd[cmd] = fn
}
