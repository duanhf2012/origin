# Redis 扩展组件示例

| 示例 | 游戏场景 | 重点 |
| --- | --- | --- |
| [缓存与会话](./01-cache-and-session/) | PB 玩家缓存、批量摘要、滑动会话、一次性 Token | Miss、空字符串、损坏缓存、TTL |
| [集合与基础排行](./02-collections-and-ranking/) | 玩家 Hash、在线 Set、匹配 List、整数 ZSet、签到 Bitmap | Scan、有界读取、整数 Score |
| [Pipeline、Lua 与并发](./03-pipeline-lua-and-concurrency/) | 批量独立读写、Watch 乐观更新、幂等奖励 Lua | 原子性、冲突重试、Hash Tag |
| [分布式 Lease Lock](./04-distributed-lock/) | 缓存重建、匹配结算、定时抢占、长任务刷新 | 未获得锁、取消、过期、最终幂等 |

四个示例都需要可用的 Redis Standalone，默认地址为 `127.0.0.1:6379`。每个目录都能独立构建、运行，
并把 Redis 业务方法集中在自己的业务 Module 中。
