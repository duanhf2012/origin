@echo off
setlocal
cd /d "%~dp0..\..\..\deploy\compose"
docker compose -f base-compose.yml ps n1 n2 n3
