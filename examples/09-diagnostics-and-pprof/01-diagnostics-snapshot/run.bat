@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/09-diagnostics-and-pprof/01-diagnostics-snapshot start --app-name diagnostics-snapshot --config ./examples/09-diagnostics-and-pprof/01-diagnostics-snapshot/config --node game-1
