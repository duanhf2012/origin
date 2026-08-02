@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/08-retire-and-resume/02-node-and-application start --app-name app-retire --config ./examples/08-retire-and-resume/02-node-and-application/config --node upstream-1,downstream-1
