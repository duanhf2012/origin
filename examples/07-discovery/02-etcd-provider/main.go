package main

import (
	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/examples/_support/tutorialwatcher"
)

var app = application.New()

func init() { app.Setup(&tutorialwatcher.Service{}) }

func main() { app.Start() }
