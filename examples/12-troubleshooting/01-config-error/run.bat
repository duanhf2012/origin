@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/00-quickstart/01-hello-service start --app-name invalid-config --config ./examples/12-troubleshooting/01-config-error/config --node Game-1
