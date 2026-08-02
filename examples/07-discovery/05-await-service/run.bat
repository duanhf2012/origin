@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/07-discovery/05-await-service start --app-name discovery-await --config ./examples/07-discovery/05-await-service/config --node gateway-1
