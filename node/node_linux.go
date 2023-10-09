// +build linux

package node

import (
	"fmt"
	"syscall"
)

func KillProcess(processId int){
	err := syscall.Kill(processId,SingleStop)
	if err != nil {
		fmt.Printf("kill processid %d is fail:%+v.\n",processId,err)
	}else{
		fmt.Printf("kill processid %d is successful.\n",processId)
	}
}

func GetBuildOSType() BuildOSType{
	return Linux
}

func RetireProcess(processId int){
	err := syscall.Kill(processId,SignalRetire)
	if err != nil {
		fmt.Printf("retire processid %d is fail:%+v.\n",processId,err)
	}else{
		fmt.Printf("retire processid %d is successful.\n",processId)
	}
}
