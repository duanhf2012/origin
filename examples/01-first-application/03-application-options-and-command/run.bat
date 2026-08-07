@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./examples/01-first-application/03-application-options-and-command print-options Alice
