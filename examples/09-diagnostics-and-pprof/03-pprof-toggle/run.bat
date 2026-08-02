@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/09-diagnostics-and-pprof/03-pprof-toggle start --app-name pprof-toggle --config ./examples/09-diagnostics-and-pprof/03-pprof-toggle/config --node game-1
