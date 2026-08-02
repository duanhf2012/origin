@echo off
setlocal
cd /d "%~dp0..\..\.."
if not exist examples\10-deployment-and-operations\01-build-and-run\bin mkdir examples\10-deployment-and-operations\01-build-and-run\bin
go build -o examples\10-deployment-and-operations\01-build-and-run\bin\hello-service.exe ./examples/00-quickstart/01-hello-service
