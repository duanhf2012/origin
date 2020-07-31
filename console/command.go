package console

import (
	"fmt"
)

type CommandFunctionCB func(args []string)error

var mapRegisterCmd map[string]CommandFunctionCB =  map[string]CommandFunctionCB{}
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

	return fn(args)
}

func RegisterCommand(cmd string,fn CommandFunctionCB){
	mapRegisterCmd[cmd] = fn
}
