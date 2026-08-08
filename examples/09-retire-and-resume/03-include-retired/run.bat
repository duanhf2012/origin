@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/09-retire-and-resume/03-include-retired start --app-name include-retired --config ./examples/09-retire-and-resume/03-include-retired/config --node game-1
