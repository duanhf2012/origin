@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/00-quickstart/01-hello-service start --app-name hello-service --config ./examples/00-quickstart/01-hello-service/config --node hello-1
