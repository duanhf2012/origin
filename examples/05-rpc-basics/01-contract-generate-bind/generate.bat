@echo off
setlocal
cd /d "%~dp0..\..\.."
go run ./cmd/origingen rpc ./examples/_support/tutorialrpc
