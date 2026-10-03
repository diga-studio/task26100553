# task26100553

ForgeLink 会话排期冲突判定库（Go，进程内内存，无外部依赖）。

## API

- `New() *Scheduler` 创建调度器（并发安全）。
- `Check(req) (Verdict, error)` 预检，不改变状态；`req.ID` 已存在时按改期语义判定。
- `Submit(req) (Verdict, error)` 登记或改期；仅无冲突时生效，改期失败保留旧预约。
- `Cancel(id) bool` 取消并立即释放全部影响。

## 规则

- 时间为整数秒半开区间 `[Start, End)`。
- 同机预约须完全错开并留足 30 秒冷却（恰好 30 秒合法，冷却不占容量）。
- `AdminExempt` 跳过同机重叠/冷却检查，不豁免容量；豁免预约仍阻塞后续普通提交并占用容量。
- 任意一秒全实例合计最多 50 条占用（`MaxConcurrentSessions`）。
- 判定失败时 `Verdict` 同时列出同机冲突与容量冲突；豁免模式只报告容量冲突。

## 测试

```sh
go test ./...
```
