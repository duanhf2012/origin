# 监听发现与 Lost

这个不依赖网络的 Provider 先发布 `player-1:PlayerService`，500ms 后提交空快照。监听 Service 会先收到 `discovered`，随后立即收到 `lost`。

```text
run.bat
```

这是状态同步示例，不做防抖；业务应把 Lost 当作及时的恢复、降级或告警输入。
