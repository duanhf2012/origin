@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/09-retire-and-resume/01-service-retire-resume start --app-name service-retire --config ./examples/09-retire-and-resume/01-service-retire-resume/config --pid-dir ./examples/09-retire-and-resume/01-service-retire-resume/run --node game-1
