@echo off
setlocal
if "%ORIGIN_MONGODB_URI%"=="" set "ORIGIN_MONGODB_URI=mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
cd /d "%~dp0..\..\.."
go run ./examples/15-mongodb/01-game-store start --app-name mongodb-game-store --config ./examples/15-mongodb/01-game-store/config --node mongodb-1
