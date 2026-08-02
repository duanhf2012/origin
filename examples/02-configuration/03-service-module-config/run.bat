@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/02-configuration/03-service-module-config start --app-name module-config --config ./examples/02-configuration/03-service-module-config/config --node game-1
