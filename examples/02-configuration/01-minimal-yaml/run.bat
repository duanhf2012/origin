@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/02-configuration/01-minimal-yaml start --app-name minimal-yaml --config ./examples/02-configuration/01-minimal-yaml/config --node game-1
