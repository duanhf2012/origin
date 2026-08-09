@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/10-admin-diagnostics-and-pprof/06-metrics-adapter start --app-name metrics-adapter --config ./examples/10-admin-diagnostics-and-pprof/06-metrics-adapter/config --node game-1
