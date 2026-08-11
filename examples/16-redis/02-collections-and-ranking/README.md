# Redis 集合与基础排行

示例覆盖玩家 Hash、在线玩家 Set、匹配候选 List、整数积分 ZSet 和签到 Bitmap。运行方式与缓存示例一致，
成功日志是 `Redis collections/ranking demo completed`。

生产注意：大 Hash/Set 用 HScan/SScan 循环；List 只适合不需要确认和重放的有界临时数据；排行便利层只
接受可精确表达的 int64，复合排序规则由业务层实现；任何 Range/Pop 都要设置业务上限。
