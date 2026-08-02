@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/01-first-application/02-lifecycle-order start --app-name lifecycle-order --config ./examples/01-first-application/02-lifecycle-order/config --node lifecycle-1
