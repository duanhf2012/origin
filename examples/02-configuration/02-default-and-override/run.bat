@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/02-configuration/02-default-and-override start --app-name config-override --config ./examples/02-configuration/02-default-and-override/config --node game-1
