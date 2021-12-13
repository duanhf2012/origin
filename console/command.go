package console

import (
	"flag"
	"fmt"
	"os"
)

type valueType int
type CommandFunctionCB func(args interface{}) error
var commandList []*command
var programName string
const(
	boolType valueType = iota
	stringType valueType = iota
)

type command struct{
	valType valueType
	name string
	bValue bool
	strValue string
	usage string
	fn CommandFunctionCB
}

func (cmd *command) execute() error{
	if cmd.valType == boolType {
		return cmd.fn(cmd.bValue)
	}else if cmd.valType == stringType {
		return cmd.fn(cmd.strValue)
	}else{
		return fmt.Errorf("Unknow command type.")
	}

	return nil
}

func Run(args []string) error {
	flag.Parse()
	programName = args[0]
	if flag.NFlag() <= 0 {
		return fmt.Errorf("Command input parameter error,try `%s -help` for help",args[0])
	}

	var startCmd *command
	for _,val := range commandList {
		if val.name == "start" {
			startCmd = val
			continue
		}
		err := val.execute()
		if err != nil {
			return err
		}
	}

	if startCmd != nil {
		return startCmd.execute()
	}

	return fmt.Errorf("Command input parameter error,try `%s -help` for help",args[0])
}

func RegisterCommandBool(cmdName string, defaultValue bool, usage string,fn CommandFunctionCB){
	var cmd command
	cmd.valType = boolType
	cmd.name = cmdName
	cmd.fn = fn
	cmd.usage = usage
	flag.BoolVar(&cmd.bValue, cmdName, defaultValue, usage)
	commandList = append(commandList,&cmd)
}

func RegisterCommandString(cmdName string, defaultValue string, usage string,fn CommandFunctionCB){
	var cmd command
	cmd.valType = stringType
	cmd.name = cmdName
	cmd.fn = fn
	cmd.usage = usage
	flag.StringVar(&cmd.strValue, cmdName, defaultValue, usage)
	commandList = append(commandList,&cmd)
}

func PrintDefaults(){
	fmt.Fprintf(os.Stderr, "Options:\n")

	for _,val := range commandList {
		fmt.Fprintf(os.Stderr, "  -%-10s%10s\n",val.name,val.usage)
	}
}

func GetParamStringVal(paramName string) string{
	for _,cmd := range commandList {
		if cmd.name == paramName{
			return cmd.strValue
		}
	}
	return ""
}

func GetParamBoolVal(paramName string) bool {
	for _,cmd := range commandList {
		if cmd.name == paramName{
			return cmd.bValue
		}
	}

	return false
}