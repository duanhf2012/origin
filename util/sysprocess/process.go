package sysprocess

import (
	"github.com/shirou/gopsutil/process"
	"os"
)

func GetProcessNameByPID(pid int32) (string, error) {
	proc, err := process.NewProcess(pid)
	if err != nil {
		return "", err
	}

	processName, err := proc.Name()
	if err != nil {
		return "", err
	}

	return processName, nil
}

func GetMyProcessName() (string, error) {
	return GetProcessNameByPID(int32(os.Getpid()))
}

