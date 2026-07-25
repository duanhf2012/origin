package log_test

import (
	"context"

	originlog "github.com/duanhf2012/origin/v3/log"
	"github.com/duanhf2012/origin/v3/log/zaplog"
)

func Example() {
	config := originlog.DefaultConfig()
	runtime, err := zaplog.New(config)
	if err != nil {
		return
	}

	logger := runtime.Logger().With(originlog.String("service", "PlayerService"))
	logger.Info("service started")
	_ = runtime.Close(context.Background())
}
