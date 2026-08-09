@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/10-admin-diagnostics-and-pprof/05-pprof-toggle start --app-name pprof-toggle --config ./examples/10-admin-diagnostics-and-pprof/05-pprof-toggle/config --node game-1 --admin 127.0.0.1:6064 --pprof 127.0.0.1:6060
