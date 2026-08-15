# RPC 按 Labels 筛选候选节点路由实施计划

> 状态：已完成
>
> 基线：v3.2.0 发布候选
>
> 目标：v3.3.0
>
> 设计依据：`../design/RPC按Labels筛选候选节点路由设计.md`

## 1. 实施顺序

1. 在 `rpc.Client` 中增加有界、不可变的 Labels 条件和值派生方法，锁定空值、合并、冲突、
   OnNode 和其他派生顺序。
2. 把条件接入现有 `candidateSet` 扫描、错误分类、Await 等待判定和 RouteCandidates 只读视图，
   不创建过滤结果列表。
3. 在 Broadcast Prepare 阶段拒绝带 Labels 的客户端，保持既有广播范围。
4. 扩展 rpcgen 强类型链式方法，ABI 保持 3，重新生成仓库正式生成物并更新公共 API 契约测试。
5. 补齐单元测试、Benchmark、分配断言和竞态验证。

## 2. 质量门禁

```text
gofmt
go test ./rpc ./internal/rpcgen ./tests/contracts
go test -race ./rpc ./internal/rpcgen
go run ./cmd/origingen rpc --check ./...
go test ./...
go vet ./...
```

Benchmark 至少记录无过滤、2 个匹配 Labels、32 个匹配 Labels，以及无匹配失败路径的
`ns/op`、`B/op` 和 `allocs/op`。如果 Prepare 成功路径出现框架新增分配，停止收口并定位，
不得用对象池或缓存掩盖回归。

## 3. 范围保护

- RPC 实施阶段不修改当时已有的 Kafka、go.mod 或其他无关工作树变更；后续 v3.3 RC
  发布审查按单独报告统一复核并收口；
- 不提升生成 ABI，不修改 Wire、错误码或服务发现模型；
- 不实现标签 Broadcast、表达式、缓存、一致性哈希或权重路由；
- 实现和测试与设计冲突时先回到设计确认，不静默扩大范围。
