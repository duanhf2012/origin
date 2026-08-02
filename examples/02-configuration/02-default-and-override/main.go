package main

import (
	"context"
	"fmt"

	"github.com/duanhf2012/origin/v3/application"
	"github.com/duanhf2012/origin/v3/service"
)

var app = application.New()

type serviceConfig struct {
	Welcome    string `json:"welcome"`
	MaxPlayers int    `json:"max_players"`
}

type ConfigService struct {
	service.Service
	config serviceConfig
}

func (target *ConfigService) OnInit() error {
	// 字段缺失时保留这里定义的业务默认值。
	target.config = serviceConfig{Welcome: "default welcome", MaxPlayers: 10}
	if err := target.ParseServiceConfig(&target.config); err != nil {
		return err
	}
	target.Logger().Info(fmt.Sprintf("welcome=%q max_players=%d", target.config.Welcome, target.config.MaxPlayers))
	return nil
}

func (target *ConfigService) OnStart(context.Context) error { return nil }

func init() { app.Setup(&ConfigService{}) }

func main() { app.Start() }
