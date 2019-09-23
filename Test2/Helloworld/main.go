package main

import (
	"github.com/duanhf2012/origin/originnode"
)

func main() {

	node := originnode.NewOriginNode()
	if node == nil {
		return
	}

	node.Init()
	node.Start()
}
