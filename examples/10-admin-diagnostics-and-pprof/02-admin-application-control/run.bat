@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/10-admin-diagnostics-and-pprof/02-admin-application-control start --app-name admin-application-control --config ./examples/10-admin-diagnostics-and-pprof/02-admin-application-control/config --node game-1 --admin 127.0.0.1:6062
