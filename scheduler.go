package scheduler

import (
	"errors"
	"sort"
	"sync"
)

// ErrInvalidRequest 表示请求本身不合法（空标识、空机器或起止时间颠倒）。
var ErrInvalidRequest = errors.New("scheduler: invalid request")

// Scheduler 保存实例内全部预约，并判定新提交是否可接受。
// 零值不可用，请使用 New 创建。并发安全。
type Scheduler struct {
	mu           sync.RWMutex
	reservations map[string]Reservation
}

// New 创建一个空的调度器。
func New() *Scheduler {
	return &Scheduler{reservations: make(map[string]Reservation)}
}

// Check 预检一次提交，不改变任何内部状态。
// 若 req.ID 已存在，则按改期语义判定（排除旧预约）。
func (s *Scheduler) Check(req Request) (Verdict, error) {
	if err := validate(req); err != nil {
		return Verdict{}, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.judge(req), nil
}

// Submit 登记新预约或对已有预约改期。
// 只有无冲突的提交才会生效；改期失败时旧预约原样保留。
func (s *Scheduler) Submit(req Request) (Verdict, error) {
	if err := validate(req); err != nil {
		return Verdict{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	v := s.judge(req)
	if !v.OK {
		return v, nil
	}
	s.reservations[req.ID] = Reservation{
		ID:        req.ID,
		MachineID: req.MachineID,
		Start:     req.Start,
		End:       req.End,
	}
	return v, nil
}

// Cancel 取消预约并立即释放其全部影响，返回是否存在该预约。
func (s *Scheduler) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.reservations[id]; !ok {
		return false
	}
	delete(s.reservations, id)
	return true
}

// Reservation 返回指定预约的副本。
func (s *Scheduler) Reservation(id string) (Reservation, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.reservations[id]
	return r, ok
}

func validate(req Request) error {
	if req.ID == "" || req.MachineID == "" || req.Start >= req.End {
		return ErrInvalidRequest
	}
	return nil
}

// judge 在不加锁的情况下判定请求。改期时 req.ID 对应的旧预约被排除。
func (s *Scheduler) judge(req Request) Verdict {
	v := Verdict{OK: true}
	if !req.AdminExempt {
		v.Machine = s.machineConflicts(req)
	}
	v.Capacity = s.capacityConflicts(req)
	v.OK = len(v.Machine) == 0 && len(v.Capacity) == 0
	return v
}

// machineConflicts 检查候选与每条同机预约是否完全错开且留足冷却。
func (s *Scheduler) machineConflicts(req Request) []MachineConflict {
	var out []MachineConflict
	for _, r := range s.reservations {
		if r.ID == req.ID || r.MachineID != req.MachineID {
			continue
		}
		// 合法条件：候选整体在已有预约之前且间隔 >= 冷却，或反之。
		if req.End+CooldownSeconds <= r.Start || r.End+CooldownSeconds <= req.Start {
			continue
		}
		reason := "cooldown"
		if req.Start < r.End && r.Start < req.End {
			reason = "overlap"
		}
		out = append(out, MachineConflict{ExistingID: r.ID, Reason: reason})
	}
	return out
}

// capacityConflicts 用扫描线统计候选时间窗内每一秒的并发占用，
// 占用数包含候选自身，超过上限的连续区间逐段报告。
func (s *Scheduler) capacityConflicts(req Request) []CapacityConflict {
	delta := make(map[int64]int)
	for _, r := range s.reservations {
		if r.ID == req.ID {
			continue
		}
		lo := max(r.Start, req.Start)
		hi := min(r.End, req.End)
		if lo < hi {
			delta[lo]++
			delta[hi]--
		}
	}
	points := make([]int64, 0, len(delta))
	for p := range delta {
		points = append(points, p)
	}
	sort.Slice(points, func(i, j int) bool { return points[i] < points[j] })

	var out []CapacityConflict
	running := 0
	open := false
	for i, p := range points {
		next := req.End
		if i+1 < len(points) {
			next = points[i+1]
		}
		running += delta[p]
		peak := running + 1 // 候选自身占用 1 个名额
		if peak > MaxConcurrentSessions && p < next {
			if open && out[len(out)-1].To == p {
				out[len(out)-1].To = next
				out[len(out)-1].Peak = max(out[len(out)-1].Peak, peak)
			} else {
				out = append(out, CapacityConflict{From: p, To: next, Peak: peak, Limit: MaxConcurrentSessions})
				open = true
			}
		} else {
			open = false
		}
	}
	return out
}
