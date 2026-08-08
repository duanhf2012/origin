@echo off
setlocal
cd /d "%~dp0..\..\..\deploy\compose"
docker compose -f base-compose.yml stop etcd1 etcd2 etcd3
