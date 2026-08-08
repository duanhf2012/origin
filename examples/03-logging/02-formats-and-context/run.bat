@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/03-logging/02-formats-and-context start --app-name format-context --config ./examples/03-logging/02-formats-and-context/config --node game-1
