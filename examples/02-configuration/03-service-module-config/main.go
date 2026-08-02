package main

import (
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type settings struct {
	Region string `json:"region"`
}

type ConfigModule struct{ service.Module }

func (target *ConfigModule) OnInit() error {
	var value settings
	if err := target.ParseServiceConfig(&value); err != nil {
		return err
	}
	fmt.Printf("module reads region=%q\n", value.Region)
	return nil
}

type ConfigService struct{ service.Service }

func (target *ConfigService) OnInit() error {
	return target.AddModule(&ConfigModule{})
}

func init() { app.Setup(&ConfigService{}) }

func main() { app.Start() }
