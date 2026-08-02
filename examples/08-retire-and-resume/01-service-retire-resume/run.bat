@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/08-retire-and-resume/01-service-retire-resume start --app-name service-retire --config ./examples/08-retire-and-resume/01-service-retire-resume/config --node game-1
