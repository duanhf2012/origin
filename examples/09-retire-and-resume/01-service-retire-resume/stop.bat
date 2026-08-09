@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/09-retire-and-resume/01-service-retire-resume stop --app-name service-retire --pid-dir ./examples/09-retire-and-resume/01-service-retire-resume/run --timeout 30s
