// Package forgelink 提供 ForgeLink 远程维护会话的排期冲突判定。
// 数据保存在进程内内存，由上层直接调用。
package forgelink

import "sync"

const (
	// CooldownSeconds 同机两条预约之间必须保留的冷却秒数，恰好等于该值合法。
	CooldownSeconds int64 = 30
	// MaxConcurrentSessions 整个实例任意一秒允许的最大并发占用数。
	MaxConcurrentSessions = 50
)

// Candidate 是一次待判定的占用请求：新登记或改期后的目标形态。
// 时间以整数秒表示，Start 计入占用，End 不计入（半开区间 [Start, End)）。
// MachineID 与 Exempt 由调用方可信提供。
type Candidate struct {
	ID        string // 预约唯一标识；改期时与已有预约同 ID 表示替换
	MachineID string
	Start     int64
	End       int64
	Exempt    bool // 管理员豁免：跳过同机重叠与冷却检查，不豁免实例容量
}

// Reservation 是已登记的预约。
type Reservation struct {
	ID        string
	MachineID string
	Start     int64
	End       int64
	Exempt    bool
}

// ConflictKind 标识冲突类别。
type ConflictKind int

const (
	// ConflictMachine 同机冲突：与已有同机预约重叠或冷却不足。
	ConflictMachine ConflictKind = iota
	// ConflictCapacity 容量冲突：某秒全实例并发占用超过上限。
	ConflictCapacity
)

// Conflict 描述一处冲突。
type Conflict struct {
	Kind          ConflictKind
	ReservationID string // 同机冲突时对方预约的 ID；容量冲突时为空
	At            int64  // 容量冲突时超标的秒；同机冲突时为 0
}

// Verdict 是判定结果。OK 为 true 表示可接受。
type Verdict struct {
	OK        bool
	Conflicts []Conflict
}

// Scheduler 保存全部已登记预约并执行冲突判定。并发安全。
type Scheduler struct {
	mu           sync.RWMutex
	reservations map[string]Reservation
}

// NewScheduler 创建一个空的 Scheduler。
func NewScheduler() *Scheduler {
	return &Scheduler{reservations: make(map[string]Reservation)}
}

// Precheck 预检：与 Commit 使用完全相同的判定语义，但不改变任何状态。
func (s *Scheduler) Precheck(c Candidate) Verdict {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return evaluate(s.reservations, c)
}

// Commit 登记或改期：无冲突时生效并返回可接受的判定；
// 有冲突时不产生任何影响，返回说明冲突的判定。
// 若 c.ID 已存在则为改期：判定时排除旧预约，成功后整体替换，失败时旧预约原样保留。
func (s *Scheduler) Commit(c Candidate) Verdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	v := evaluate(s.reservations, c)
	if !v.OK {
		return v
	}
	s.reservations[c.ID] = Reservation{
		ID:        c.ID,
		MachineID: c.MachineID,
		Start:     c.Start,
		End:       c.End,
		Exempt:    c.Exempt,
	}
	return v
}

// Cancel 取消预约，立即释放其全部影响。返回该预约是否存在过。
func (s *Scheduler) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.reservations[id]; !ok {
		return false
	}
	delete(s.reservations, id)
	return true
}
