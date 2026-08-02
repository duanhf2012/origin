@echo off
setlocal
cd /d "%~dp0..\..\..\.."
go run ./examples/_baseline/v3.0/m7-lifecycle start --app-name lifecycle-demo --config ./examples/_baseline/v3.0/m7-lifecycle/config --node gateway-1,game-1
