# 业务监控适配

框架只提供 `diagnostics.Source` 和统一快照；该示例将两项数值写到控制台 `GaugeSink`。业务可将同一适配层替换为 Prometheus、OpenTelemetry 或公司监控 SDK，而无需把监控接口散落到 Node、RPC、Service 等多个包。

```text
run.bat
```
