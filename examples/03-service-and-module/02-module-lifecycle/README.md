# Module 生命周期

`Service.OnInit` 中添加根 Module；根 Module 的 `OnInit` 中添加子 Module。启动按父到子顺序，停止按子到父顺序。

```text
run.bat
```

按 `Ctrl+C`，比较 `root` 与 `child` 的停止日志。
