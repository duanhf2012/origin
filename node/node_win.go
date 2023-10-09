// +build windows

package node

import (
	"os"
	"fmt"
)

func KillProcess(processId int){
	procss,err := os.FindProcess(processId)
	if err != nil {
		fmt.Printf("kill processid %d is fail:%+v.\n",processId,err)
		return
	}

	err = procss.Kill()
	if err != nil {
		fmt.Printf("kill processid %d is fail:%+v.\n",processId,err)
	}
}

func GetBuildOSType() BuildOSType{
	return Windows
}

func RetireProcess(processId int){
	fmt.Printf("This command does not support Windows")
}
