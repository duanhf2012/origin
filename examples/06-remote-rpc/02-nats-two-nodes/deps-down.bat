@echo off
setlocal
cd /d "%~dp0..\..\..\deploy\compose"
docker compose -f base-compose.yml stop n1 n2 n3
