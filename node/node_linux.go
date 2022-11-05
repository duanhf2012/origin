// +build linux

package node

import (
	"fmt"
	"syscall"
)

func KillProcess(processId int){
	err := syscall.Kill(processId,syscall.Signal(10))
	if err != nil {
		fmt.Printf("kill processid %d is fail:%+v.\n",processId,err)
	}else{
		fmt.Printf("kill processid %d is successful.\n",processId)
	}
}

func GetBuildOSType() BuildOSType{
	return Linux
}
