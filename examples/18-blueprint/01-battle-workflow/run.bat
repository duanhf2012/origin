@echo off
setlocal
cd /d "%~dp0\..\..\.."
go run ./examples/18-blueprint/01-battle-workflow start --app-name blueprint-example --config ./examples/18-blueprint/01-battle-workflow/config --node blueprint-1
