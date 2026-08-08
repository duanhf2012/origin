@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/12-logging/04-file-rotation start --app-name log-output --config ./examples/12-logging/04-file-rotation/config --node log-1
