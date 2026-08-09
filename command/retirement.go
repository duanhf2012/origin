package command

import (
	"context"
	"flag"
	"io"
	"time"

	"github.com/duanhf2012/origin/v3/errs"
)

type controlTarget struct {
	appName string
	pidDir  string
	exists  bool
	timeout time.Duration
}

func (runner *Runner) runRetire(ctx context.Context, args []string) (ExitCode, error) {
	return runner.runApplicationControl(ctx, "retire", ControlActionRetire, args)
}

func (runner *Runner) runResume(ctx context.Context, args []string) (ExitCode, error) {
	return runner.runApplicationControl(ctx, "resume", ControlActionResume, args)
}

func (runner *Runner) runApplicationControl(
	ctx context.Context,
	commandName string,
	action ControlAction,
	args []string,
) (ExitCode, error) {
	if containsHelpFlag(args) {
		text, _ := runner.builtInHelp(commandName)
		return runner.writeHelpText(text)
	}
	target, code, err := runner.parseControlTarget(commandName, args)
	if err != nil {
		return code, err
	}
	if !target.exists {
		return ExitProcessControl, processControlf("application %q is not running", target.appName)
	}

	controlCtx, cancel := context.WithTimeout(ctx, target.timeout)
	defer cancel()
	err = requestApplicationControl(controlCtx, target.pidDir, target.appName, action)
	if err == nil {
		return ExitSuccess, nil
	}
	switch errs.CodeOf(err) {
	case errs.CodeDeadlineExceeded:
		return ExitControlTimeout, err
	case errs.CodeProcessControlFailed, errs.CodeProcessAlreadyRunning:
		return ExitProcessControl, err
	default:
		return ExitFailure, err
	}
}

func (runner *Runner) parseControlTarget(
	commandName string,
	args []string,
) (controlTarget, ExitCode, error) {
	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	appName := flags.String("app-name", "", "Application 名称")
	pidDir := flags.String("pid-dir", "./run", "PID 目录")
	timeoutText := flags.String("timeout", defaultControlTimeout.String(), "命令的总等待时间")
	if err := flags.Parse(args); err != nil {
		return controlTarget{}, ExitUsage, invalidArgumentf("parse %s arguments: %v", commandName, err)
	}
	if err := rejectPositionals(flags); err != nil {
		return controlTarget{}, ExitUsage, err
	}
	if err := validateKebabName(*appName, "app name"); err != nil {
		return controlTarget{}, ExitUsage, err
	}
	timeout, err := time.ParseDuration(*timeoutText)
	if err != nil || timeout <= 0 {
		return controlTarget{}, ExitUsage, invalidArgumentf(
			"%s timeout %q must be a positive duration",
			commandName,
			*timeoutText,
		)
	}
	absolutePIDDir, exists, err := resolvePIDDirForStop(*pidDir)
	if err != nil {
		return controlTarget{}, ExitProcessControl, err
	}
	return controlTarget{
		appName: *appName,
		pidDir:  absolutePIDDir,
		exists:  exists,
		timeout: timeout,
	}, ExitSuccess, nil
}
