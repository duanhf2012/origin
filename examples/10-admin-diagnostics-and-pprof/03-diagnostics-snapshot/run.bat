@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/10-admin-diagnostics-and-pprof/03-diagnostics-snapshot start --app-name diagnostics-snapshot --config ./examples/10-admin-diagnostics-and-pprof/03-diagnostics-snapshot/config --node game-1
