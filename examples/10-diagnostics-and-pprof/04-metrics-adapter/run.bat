@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/10-diagnostics-and-pprof/04-metrics-adapter start --app-name metrics-adapter --config ./examples/10-diagnostics-and-pprof/04-metrics-adapter/config --node game-1
