@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/03-logging/03-file-rotation start --app-name log-output --config ./examples/03-logging/03-file-rotation/config --node log-1
