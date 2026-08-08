@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/03-logging/04-runtime-control start --app-name runtime-control --config ./examples/03-logging/04-runtime-control/config --node game-1
