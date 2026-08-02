@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/07-discovery/04-custom-provider start --app-name custom-provider --config ./examples/07-discovery/04-custom-provider/config --node game-1
