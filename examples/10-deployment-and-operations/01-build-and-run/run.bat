@echo off
setlocal
call "%~dp0build.bat"
cd /d "%~dp0..\..\.."
examples\10-deployment-and-operations\01-build-and-run\bin\hello-service.exe start --app-name deployed-hello --config ./examples/00-quickstart/01-hello-service/config --node hello-1
