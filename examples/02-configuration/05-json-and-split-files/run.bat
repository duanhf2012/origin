@echo off
setlocal
set "ORIGIN_TUTORIAL_REGION=cn-east"
cd /d "%~dp0..\..\.."
go run ./examples/02-configuration/05-json-and-split-files start --app-name split-config --config ./examples/02-configuration/05-json-and-split-files/config --node game-1,game-2
