package main

import (
	"fmt"

	"github.com/duanhf2012/origin/v3/buildinfo"
	"github.com/duanhf2012/origin/v3/errs"
)

func main() {
	fmt.Println("Origin v3 basic example")
	fmt.Printf(
		"build: version=%q commit=%q time=%q\n",
		buildinfo.Version(),
		buildinfo.Commit(),
		buildinfo.BuildTime(),
	)

	err := errs.NewMessage(errs.CodeInvalidArgument, "player ID is empty")
	fmt.Printf("error: code=%d message=%q\n", errs.CodeOf(err), err)
}
