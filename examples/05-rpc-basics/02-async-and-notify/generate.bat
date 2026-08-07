@echo off
setlocal
cd /d "%~dp0..\..\.."
go generate ./examples/_support/tutorialrpc
