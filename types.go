// Package scheduler 提供 ForgeLink 远程维护会话的排期冲突判定。
// 数据保存在进程内内存，由上层直接调用。
package scheduler

// CooldownSeconds 是同机两条预约之间必须保留的冷却秒数。
// 恰好 30 秒合法，冷却区间不占用实例会话名额。
const CooldownSeconds = 30

// MaxConcurrentSessions 是整个实例任意一秒允许的最大并发占用数。
const MaxConcurrentSessions = 50

// Reservation 是一条已登记的预约，时间以整数秒表示，
// 开始秒计入占用，结束秒不计入（半开区间 [Start, End)）。
type Reservation struct {
	ID        string
	MachineID string
	Start     int64
	End       int64
}

// Request 是一次判定请求。AdminExempt 为 true 时跳过同机重叠与
// 冷却检查，但不豁免实例容量限制。
type Request struct {
	// ID 为预约唯一标识。改期时填写已有预约的 ID。
	ID string
	// MachineID 为目标机器，由调用方可信提供。
	MachineID string
	Start     int64
	End       int64
	// AdminExempt 为管理员豁免标记，由调用方可信提供。
	AdminExempt bool
}

// ConflictKind 标识冲突类别。
type ConflictKind int

const (
	// ConflictMachine 表示同机重叠或冷却不足。
	ConflictMachine ConflictKind = iota + 1
	// ConflictCapacity 表示实例容量超限。
	ConflictCapacity
)

// MachineConflict 描述与一条已有同机预约的冲突。
type MachineConflict struct {
	ExistingID string
	Reason     string
}

// CapacityConflict 描述候选时间窗内容量超限的区间。
type CapacityConflict struct {
	// From 与 To 为超限秒区间的半开范围 [From, To)。
	From  int64
	To    int64
	Peak  int
	Limit int
}

// Verdict 是一次判定的结果。OK 为 true 时表示可接受。
type Verdict struct {
	OK       bool
	Machine  []MachineConflict
	Capacity []CapacityConflict
}

// Has 报告是否存在指定类别的冲突。
func (v Verdict) Has(kind ConflictKind) bool {
	switch kind {
	case ConflictMachine:
		return len(v.Machine) > 0
	case ConflictCapacity:
		return len(v.Capacity) > 0
	}
	return false
}
