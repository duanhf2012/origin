@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/10-admin-diagnostics-and-pprof/01-admin-service-endpoints start --app-name admin-service-endpoints --config ./examples/10-admin-diagnostics-and-pprof/01-admin-service-endpoints/config --node game-1 --admin 127.0.0.1:6061
