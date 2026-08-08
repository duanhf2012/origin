@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/03-logging/05-custom-handler start --app-name custom-handler --config ./examples/03-logging/05-custom-handler/config --node handler-1
