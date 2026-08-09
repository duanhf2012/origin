@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/10-admin-diagnostics-and-pprof/07-admin-toggle start --app-name admin-toggle --config ./examples/10-admin-diagnostics-and-pprof/07-admin-toggle/config --node game-1
