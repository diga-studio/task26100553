# task26100553
## ForgeLink 会话排期冲突判定库

纯进程内内存实现，由上层直接调用，无外部依赖。

### 规则

- 预约占用半开区间 `[Start, End)`（整数秒）。
- 同机两条预约之间至少留 30 秒冷却，恰好 30 秒合法；冷却不占实例会话名额。
- 任意一秒全实例最多 50 条预约同时占用。
- 管理员豁免（`Exempt: true`）跳过同机重叠与冷却检查，但不豁免容量；
  豁免登记的预约仍阻塞后续普通提交并正常占用容量。

### API

```go
s := forgelink.NewScheduler()
v := s.Precheck(c)   // 预检：与提交完全相同的判定语义，不改变状态
v := s.Commit(c)     // 登记或改期（c.ID 已存在即改期，判定排除旧预约，
                     // 成功整体替换，失败旧预约原样保留）
ok := s.Cancel(id)   // 取消并立即释放全部影响
```

`Verdict.OK` 为 false 时，`Verdict.Conflicts` 同时列出同机冲突
（`ConflictMachine`，含对方预约 ID）与容量冲突（`ConflictCapacity`，
`At` 为连续超标区间的起始秒）；豁免模式下只报告容量冲突。

### 测试

```sh
go test ./...
```
