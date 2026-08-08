@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/04-service-and-module/01-first-service start --app-name first-service --config ./examples/04-service-and-module/01-first-service/config --node game-1
