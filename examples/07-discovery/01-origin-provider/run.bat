@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/07-discovery/01-origin-provider start --app-name origin-discovery --config ./examples/07-discovery/01-origin-provider/config --node discovery-1,game-1
