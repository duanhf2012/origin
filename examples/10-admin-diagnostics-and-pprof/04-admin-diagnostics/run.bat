@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/10-admin-diagnostics-and-pprof/04-admin-diagnostics start --app-name admin-diagnostics --config ./examples/10-admin-diagnostics-and-pprof/04-admin-diagnostics/config --node game-1 --admin 127.0.0.1:6063
