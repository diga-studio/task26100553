package forgelink

import "sort"

// evaluate 对候选占用执行完整判定，reservations 为当前全部已登记预约。
// c.ID 与已有预约相同时视为改期，判定中排除该旧预约。
// 预检与提交共用本函数，保证语义完全一致。
func evaluate(reservations map[string]Reservation, c Candidate) Verdict {
	var conflicts []Conflict
	if !c.Exempt {
		conflicts = append(conflicts, machineConflicts(reservations, c)...)
	}
	conflicts = append(conflicts, capacityConflicts(reservations, c)...)
	return Verdict{OK: len(conflicts) == 0, Conflicts: conflicts}
}

// machineConflicts 检查候选与每条已有同机预约（排除自身）是否重叠或冷却不足。
// 两条占用之间至少留 CooldownSeconds 秒，恰好等于该值合法。
func machineConflicts(reservations map[string]Reservation, c Candidate) []Conflict {
	var out []Conflict
	for _, r := range reservations {
		if r.ID == c.ID || r.MachineID != c.MachineID {
			continue
		}
		// 候选在前：要求 r.Start - c.End >= CooldownSeconds
		// 候选在后：要求 c.Start - r.End >= CooldownSeconds
		if c.End+CooldownSeconds <= r.Start || r.End+CooldownSeconds <= c.Start {
			continue
		}
		out = append(out, Conflict{Kind: ConflictMachine, ReservationID: r.ID})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReservationID < out[j].ReservationID })
	return out
}

// capacityConflicts 用扫描线统计候选时间窗内每一秒的全实例并发占用，
// 任一秒加上候选后超过 MaxConcurrentSessions 即构成容量冲突。
// 每段连续超标区间报告一条冲突，At 为该段起始秒。
func capacityConflicts(reservations map[string]Reservation, c Candidate) []Conflict {
	if c.End <= c.Start {
		return nil
	}
	type event struct {
		t     int64
		delta int
	}
	events := make([]event, 0, 2*len(reservations))
	for _, r := range reservations {
		if r.ID == c.ID {
			continue
		}
		events = append(events, event{r.Start, 1}, event{r.End, -1})
	}
	sort.Slice(events, func(i, j int) bool { return events[i].t < events[j].t })

	var out []Conflict
	count := 0
	overStart := int64(-1)
	flush := func(end int64) {
		if overStart >= 0 && end > overStart {
			out = append(out, Conflict{Kind: ConflictCapacity, At: overStart})
		}
		overStart = -1
	}
	for i := 0; i < len(events); {
		t := events[i].t
		// 上一段区间为 [prev, t)，count 为其并发数；先处理区间再应用 t 处事件。
		for i < len(events) && events[i].t == t {
			count += events[i].delta
			i++
		}
		next := int64(-1)
		if i < len(events) {
			next = events[i].t
		}
		// 当前区间 [t, next) 与候选窗口的交集。
		lo := max(t, c.Start)
		hi := c.End
		if next >= 0 && next < hi {
			hi = next
		}
		if lo < hi && count+1 > MaxConcurrentSessions {
			if overStart < 0 {
				overStart = lo
			}
		} else {
			flush(lo)
		}
	}
	flush(c.End)
	return out
}
