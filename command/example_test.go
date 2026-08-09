package command_test

import (
	"bytes"
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/command"
)

func ExampleRunner_Run() {
	// 最终程序把 Application 生命周期接入 Start；本例只展示不会启动业务资源的帮助命令。
	var output bytes.Buffer
	runner, err := command.New(command.Options{
		ProgramName: "game-server",
		Stdout:      &output,
		Start: func(context.Context, command.StartRequest) error {
			return nil
		},
	})
	if err != nil {
		panic(err)
	}

	code, err := runner.Run(context.Background(), []string{"help", "start"})
	fmt.Print(output.String())
	fmt.Printf("code=%d error=%v\n", code, err)

	// Output:
	// Usage:
	//   game-server start --app-name <name> [--config ./config] [--pid-dir ./run] [--node id1,id2] [--admin 127.0.0.1:6061] [--pprof 127.0.0.1:6060]
	// code=0 error=<nil>
}
