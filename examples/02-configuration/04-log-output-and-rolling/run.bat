@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/02-configuration/04-log-output-and-rolling start --app-name log-output --config ./examples/02-configuration/04-log-output-and-rolling/config --node log-1
