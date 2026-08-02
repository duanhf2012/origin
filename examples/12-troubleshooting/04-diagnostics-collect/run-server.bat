@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/09-diagnostics-and-pprof/02-diagnostics-server start --app-name diagnostics-lab --config ./examples/09-diagnostics-and-pprof/02-diagnostics-server/config --node game-1
