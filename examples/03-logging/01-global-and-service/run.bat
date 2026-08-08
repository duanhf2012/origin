@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/03-logging/01-global-and-service start --app-name logging-scope --config ./examples/03-logging/01-global-and-service/config --node game-1
