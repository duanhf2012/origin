@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/03-service-and-module/02-module-lifecycle start --app-name module-lifecycle --config ./examples/03-service-and-module/02-module-lifecycle/config --node game-1
