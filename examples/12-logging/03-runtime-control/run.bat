@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/12-logging/03-runtime-control start --app-name runtime-control --config ./examples/12-logging/03-runtime-control/config --node game-1
