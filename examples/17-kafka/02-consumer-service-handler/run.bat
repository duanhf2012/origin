@echo off
setlocal
if "%ORIGIN_KAFKA_BROKERS%"=="" set "ORIGIN_KAFKA_BROKERS=192.168.8.3:9092"
cd /d "%~dp0..\..\.."
go run ./examples/17-kafka/02-consumer-service-handler start --app-name kafka-consumer --config ./examples/17-kafka/02-consumer-service-handler/config --node kafka-consumer-1
