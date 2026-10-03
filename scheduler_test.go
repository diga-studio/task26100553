package scheduler

import "testing"

func mustSubmit(t *testing.T, s *Scheduler, req Request) {
	t.Helper()
	v, err := s.Submit(req)
	if err != nil {
		t.Fatalf("Submit(%+v) error: %v", req, err)
	}
	if !v.OK {
		t.Fatalf("Submit(%+v) rejected: %+v", req, v)
	}
}

func mustConflict(t *testing.T, s *Scheduler, req Request) Verdict {
	t.Helper()
	v, err := s.Submit(req)
	if err != nil {
		t.Fatalf("Submit(%+v) error: %v", req, err)
	}
	if v.OK {
		t.Fatalf("Submit(%+v) unexpectedly accepted", req)
	}
	return v
}

// 普通提交与已有同机预约重叠时判定冲突。
func TestOverlapSameMachine(t *testing.T) {
	s := New()
	mustSubmit(t, s, Request{ID: "a", MachineID: "m1", Start: 100, End: 200})
	v := mustConflict(t, s, Request{ID: "b", MachineID: "m1", Start: 150, End: 300})
	if !v.Has(ConflictMachine) || v.Has(ConflictCapacity) {
		t.Fatalf("want machine conflict only, got %+v", v)
	}
	if v.Machine[0].ExistingID != "a" || v.Machine[0].Reason != "overlap" {
		t.Fatalf("unexpected machine conflict detail: %+v", v.Machine[0])
	}
}

// 首尾相接（间隔 0）与冷却不足 30 秒均判定冲突，恰好 30 秒合法。
func TestCooldownBoundary(t *testing.T) {
	s := New()
	mustSubmit(t, s, Request{ID: "a", MachineID: "m1", Start: 100, End: 200})

	v := mustConflict(t, s, Request{ID: "adjacent", MachineID: "m1", Start: 200, End: 300})
	if v.Machine[0].Reason != "cooldown" {
		t.Fatalf("adjacent window should be cooldown conflict, got %+v", v.Machine[0])
	}
	mustConflict(t, s, Request{ID: "gap29", MachineID: "m1", Start: 229, End: 300})
	mustConflict(t, s, Request{ID: "gap29-before", MachineID: "m1", Start: 0, End: 71})

	mustSubmit(t, s, Request{ID: "gap30-after", MachineID: "m1", Start: 230, End: 300})
	mustSubmit(t, s, Request{ID: "gap30-before", MachineID: "m1", Start: 0, End: 70})
}

// 不同机器之间不存在同机冲突与冷却约束。
func TestDifferentMachinesNoCooldown(t *testing.T) {
	s := New()
	mustSubmit(t, s, Request{ID: "a", MachineID: "m1", Start: 100, End: 200})
	mustSubmit(t, s, Request{ID: "b", MachineID: "m2", Start: 100, End: 200})
}

// 候选时间窗内即使只短暂超过 50 条同时占用也应拒绝。
func TestCapacityBriefExceed(t *testing.T) {
	s := New()
	for i := 0; i < MaxConcurrentSessions; i++ {
		mustSubmit(t, s, Request{ID: string(rune('a'+i)) + "-bulk", MachineID: "m" + string(rune('a'+i)), Start: 1000, End: 2000})
	}
	v := mustConflict(t, s, Request{ID: "peak", MachineID: "solo", Start: 1999, End: 2000})
	if !v.Has(ConflictCapacity) || v.Has(ConflictMachine) {
		t.Fatalf("want capacity conflict only, got %+v", v)
	}
	if v.Capacity[0].From != 1999 || v.Capacity[0].To != 2000 || v.Capacity[0].Peak != MaxConcurrentSessions+1 {
		t.Fatalf("unexpected capacity detail: %+v", v.Capacity[0])
	}
}

// 候选跨过大量串行预约但同时占用未超过 50 条时，不应误报容量冲突。
func TestCapacitySerialReservations(t *testing.T) {
	s := New()
	for i := 0; i < 200; i++ {
		start := int64(i * 100)
		mustSubmit(t, s, Request{ID: "serial-" + string(rune(i)), MachineID: "m" + string(rune(i%7)), Start: start, End: start + 50})
	}
	v, err := s.Check(Request{ID: "span", MachineID: "fresh", Start: 0, End: 20000})
	if err != nil {
		t.Fatal(err)
	}
	if !v.OK {
		t.Fatalf("serial reservations must not trigger capacity conflict, got %+v", v)
	}
}

// 恰好 50 条同时占用时仍可接受（候选为第 50 条）。
func TestCapacityExactlyAtLimit(t *testing.T) {
	s := New()
	for i := 0; i < MaxConcurrentSessions-1; i++ {
		mustSubmit(t, s, Request{ID: "bulk-" + string(rune('a'+i)), MachineID: "m" + string(rune('a'+i)), Start: 0, End: 100})
	}
	mustSubmit(t, s, Request{ID: "fiftieth", MachineID: "solo", Start: 0, End: 100})
}

// 改期同时更换机器和时间，新旧时间窗部分重合时旧预约不能重复计数。
func TestRescheduleOverlapNoDoubleCount(t *testing.T) {
	s := New()
	// 49 条其它预约占满 [0,200)，加上 x 自身共 50 条。
	for i := 0; i < MaxConcurrentSessions-1; i++ {
		mustSubmit(t, s, Request{ID: "bulk-" + string(rune('a'+i)), MachineID: "m" + string(rune('a'+i)), Start: 0, End: 200})
	}
	mustSubmit(t, s, Request{ID: "x", MachineID: "old", Start: 0, End: 100})

	// 改期到 [50,150)：若旧预约被重复计数，[50,100) 将达到 51 条。
	mustSubmit(t, s, Request{ID: "x", MachineID: "new", Start: 50, End: 150})
	r, ok := s.Reservation("x")
	if !ok || r.MachineID != "new" || r.Start != 50 || r.End != 150 {
		t.Fatalf("reschedule did not replace reservation, got %+v ok=%v", r, ok)
	}
}

// 改期失败后旧预约原样保留并继续阻塞原位置。
func TestRescheduleFailureKeepsOld(t *testing.T) {
	s := New()
	mustSubmit(t, s, Request{ID: "x", MachineID: "m1", Start: 100, End: 200})
	mustSubmit(t, s, Request{ID: "y", MachineID: "m2", Start: 500, End: 600})

	// 改期到与 y 重叠的位置，应失败。
	mustConflict(t, s, Request{ID: "x", MachineID: "m2", Start: 550, End: 650})

	r, ok := s.Reservation("x")
	if !ok || r.MachineID != "m1" || r.Start != 100 || r.End != 200 {
		t.Fatalf("failed reschedule must keep old reservation, got %+v ok=%v", r, ok)
	}
	// 旧位置（含冷却）仍被阻塞。
	mustConflict(t, s, Request{ID: "z", MachineID: "m1", Start: 150, End: 250})
	mustConflict(t, s, Request{ID: "z2", MachineID: "m1", Start: 220, End: 300})
}

// 改期成功后原位置及其冷却影响正确释放。
func TestRescheduleReleasesOldWindow(t *testing.T) {
	s := New()
	mustSubmit(t, s, Request{ID: "x", MachineID: "m1", Start: 100, End: 200})
	mustSubmit(t, s, Request{ID: "x", MachineID: "m2", Start: 1000, End: 1100})

	// 原位置及其冷却区间现在可接受新预约。
	mustSubmit(t, s, Request{ID: "z", MachineID: "m1", Start: 100, End: 200})
}

// 取消后立即释放该预约的全部影响。
func TestCancelReleases(t *testing.T) {
	s := New()
	mustSubmit(t, s, Request{ID: "x", MachineID: "m1", Start: 100, End: 200})
	if !s.Cancel("x") {
		t.Fatal("Cancel should report existing reservation")
	}
	if s.Cancel("x") {
		t.Fatal("Cancel of missing reservation should be false")
	}
	mustSubmit(t, s, Request{ID: "z", MachineID: "m1", Start: 100, End: 200})
}

// 管理员豁免跳过同机重叠与冷却检查，但不豁免容量。
func TestAdminExempt(t *testing.T) {
	s := New()
	mustSubmit(t, s, Request{ID: "a", MachineID: "m1", Start: 100, End: 200})

	// 豁免提交可与同机预约重叠。
	mustSubmit(t, s, Request{ID: "admin", MachineID: "m1", Start: 150, End: 250, AdminExempt: true})

	// 豁免登记的预约仍是后续普通提交必须避让的预约。
	v := mustConflict(t, s, Request{ID: "c", MachineID: "m1", Start: 240, End: 300})
	if v.Machine[0].ExistingID != "admin" {
		t.Fatalf("exempt reservation must block later submits, got %+v", v.Machine)
	}
}

// 豁免模式只报告容量冲突。
func TestAdminExemptReportsCapacityOnly(t *testing.T) {
	s := New()
	for i := 0; i < MaxConcurrentSessions-1; i++ {
		mustSubmit(t, s, Request{ID: "bulk-" + string(rune('a'+i)), MachineID: "m" + string(rune('a'+i)), Start: 0, End: 100})
	}
	// 与同机预约重叠且容量超限，豁免模式下只报告容量冲突。
	mustSubmit(t, s, Request{ID: "on-m1", MachineID: "target", Start: 0, End: 100})
	v := mustConflict(t, s, Request{ID: "admin", MachineID: "target", Start: 50, End: 60, AdminExempt: true})
	if v.Has(ConflictMachine) || !v.Has(ConflictCapacity) {
		t.Fatalf("exempt mode must report capacity only, got %+v", v)
	}
}

// 两类冲突同时存在时必须都说明。
func TestBothConflictsReported(t *testing.T) {
	s := New()
	for i := 0; i < MaxConcurrentSessions-1; i++ {
		mustSubmit(t, s, Request{ID: "bulk-" + string(rune('a'+i)), MachineID: "m" + string(rune('a'+i)), Start: 0, End: 100})
	}
	mustSubmit(t, s, Request{ID: "mate", MachineID: "target", Start: 0, End: 100})
	v := mustConflict(t, s, Request{ID: "bad", MachineID: "target", Start: 50, End: 60})
	if !v.Has(ConflictMachine) || !v.Has(ConflictCapacity) {
		t.Fatalf("both conflicts must be reported, got %+v", v)
	}
}

// 预检与提交使用相同语义，且预检不改变后续判定结果。
func TestCheckIsSideEffectFree(t *testing.T) {
	s := New()
	mustSubmit(t, s, Request{ID: "a", MachineID: "m1", Start: 100, End: 200})

	req := Request{ID: "b", MachineID: "m1", Start: 150, End: 250}
	v1, err := s.Check(req)
	if err != nil || v1.OK {
		t.Fatalf("Check should reject conflicting request, got %+v err=%v", v1, err)
	}
	v2, err := s.Submit(req)
	if err != nil || v2.OK {
		t.Fatalf("Submit should agree with Check, got %+v err=%v", v2, err)
	}

	// 无冲突的预检不登记预约，后续相同提交仍可接受。
	okReq := Request{ID: "c", MachineID: "m1", Start: 500, End: 600}
	for i := 0; i < 2; i++ {
		v, err := s.Check(okReq)
		if err != nil || !v.OK {
			t.Fatalf("repeated Check should stay acceptable, got %+v err=%v", v, err)
		}
	}
	mustSubmit(t, s, okReq)

	// 针对改期的预检同样不改变状态。
	if _, err := s.Check(Request{ID: "c", MachineID: "m1", Start: 700, End: 800}); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Reservation("c")
	if r.Start != 500 || r.End != 600 {
		t.Fatalf("Check for reschedule must not mutate state, got %+v", r)
	}
}

// 非法请求返回错误。
func TestInvalidRequest(t *testing.T) {
	s := New()
	for _, req := range []Request{
		{ID: "", MachineID: "m1", Start: 0, End: 1},
		{ID: "a", MachineID: "", Start: 0, End: 1},
		{ID: "a", MachineID: "m1", Start: 10, End: 10},
		{ID: "a", MachineID: "m1", Start: 10, End: 5},
	} {
		if _, err := s.Check(req); err != ErrInvalidRequest {
			t.Fatalf("Check(%+v) want ErrInvalidRequest, got %v", req, err)
		}
		if _, err := s.Submit(req); err != ErrInvalidRequest {
			t.Fatalf("Submit(%+v) want ErrInvalidRequest, got %v", req, err)
		}
	}
}
