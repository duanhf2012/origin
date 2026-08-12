@echo off
setlocal
if "%ORIGIN_KAFKA_BROKERS%"=="" set "ORIGIN_KAFKA_BROKERS=192.168.8.3:9092"
cd /d "%~dp0..\..\.."
go run ./examples/17-kafka/03-managed-and-native start --app-name kafka-tools --config ./examples/17-kafka/03-managed-and-native/config --node kafka-tools-1
