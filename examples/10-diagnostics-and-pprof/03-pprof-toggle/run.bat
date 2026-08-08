@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/10-diagnostics-and-pprof/03-pprof-toggle start --app-name pprof-toggle --config ./examples/10-diagnostics-and-pprof/03-pprof-toggle/config --node game-1 --pprof 127.0.0.1:6060
