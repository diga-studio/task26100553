package forgelink

import "testing"
import "fmt"

func mustCommit(t *testing.T, s *Scheduler, c Candidate) {
	t.Helper()
	if v := s.Commit(c); !v.OK {
		t.Fatalf("Commit(%+v) unexpectedly rejected: %+v", c, v.Conflicts)
	}
}

func hasConflict(v Verdict, kind ConflictKind) bool {
	for _, c := range v.Conflicts {
		if c.Kind == kind {
			return true
		}
	}
	return false
}

func TestMachineOverlapAdjacentAndCooldown(t *testing.T) {
	s := NewScheduler()
	mustCommit(t, s, Candidate{ID: "r1", MachineID: "A", Start: 100, End: 200})

	cases := []struct {
		name string
		c    Candidate
		ok   bool
	}{
		{"overlap", Candidate{ID: "c1", MachineID: "A", Start: 150, End: 250}, false},
		{"adjacent", Candidate{ID: "c2", MachineID: "A", Start: 200, End: 300}, false},
		{"cooldown29", Candidate{ID: "c3", MachineID: "A", Start: 229, End: 300}, false},
		{"cooldown29before", Candidate{ID: "c4", MachineID: "A", Start: 0, End: 71}, false},
		{"cooldown30", Candidate{ID: "c5", MachineID: "A", Start: 230, End: 300}, true},
		{"cooldown30before", Candidate{ID: "c6", MachineID: "A", Start: 0, End: 70}, true},
		{"otherMachine", Candidate{ID: "c7", MachineID: "B", Start: 150, End: 250}, true},
	}
	for _, tc := range cases {
		v := s.Precheck(tc.c)
		if v.OK != tc.ok {
			t.Errorf("%s: Precheck OK=%v want %v (conflicts=%+v)", tc.name, v.OK, tc.ok, v.Conflicts)
		}
		if !tc.ok && !hasConflict(v, ConflictMachine) {
			t.Errorf("%s: expected machine conflict, got %+v", tc.name, v.Conflicts)
		}
		if tc.ok && len(v.Conflicts) != 0 {
			t.Errorf("%s: expected no conflicts, got %+v", tc.name, v.Conflicts)
		}
	}
}

func TestCapacityExceededBriefly(t *testing.T) {
	s := NewScheduler()
	for i := 0; i < MaxConcurrentSessions; i++ {
		mustCommit(t, s, Candidate{ID: fmt.Sprintf("base-%d", i), MachineID: fmt.Sprintf("M%d", i), Start: 0, End: 1000})
	}
	v := s.Precheck(Candidate{ID: "extra", MachineID: "Z", Start: 100, End: 200})
	if v.OK {
		t.Fatal("expected rejection when 51st concurrent session appears briefly")
	}
	if !hasConflict(v, ConflictCapacity) {
		t.Fatalf("expected capacity conflict, got %+v", v.Conflicts)
	}
	if hasConflict(v, ConflictMachine) {
		t.Fatalf("unexpected machine conflict, got %+v", v.Conflicts)
	}
}

func TestSerialReservationsNoFalseCapacity(t *testing.T) {
	s := NewScheduler()
	// 200 条串行预约，任意一秒最多 1 条占用。
	for i := 0; i < 200; i++ {
		start := int64(i * 10)
		mustCommit(t, s, Candidate{ID: fmt.Sprintf("s-%d", i), MachineID: fmt.Sprintf("M%d", i), Start: start, End: start + 10})
	}
	v := s.Precheck(Candidate{ID: "span", MachineID: "SPAN", Start: 0, End: 2000})
	if !v.OK {
		t.Fatalf("serial reservations must not trigger capacity conflict, got %+v", v.Conflicts)
	}
}

func TestRescheduleMoveMachineAndTime(t *testing.T) {
	s := NewScheduler()
	mustCommit(t, s, Candidate{ID: "r1", MachineID: "A", Start: 100, End: 200})

	// 改期：换机器且新旧窗口部分重合，旧预约不得重复计数。
	v := s.Commit(Candidate{ID: "r1", MachineID: "B", Start: 150, End: 250})
	if !v.OK {
		t.Fatalf("reschedule should succeed, got %+v", v.Conflicts)
	}
	// 原位置及其冷却影响已释放。
	if v := s.Precheck(Candidate{ID: "c1", MachineID: "A", Start: 100, End: 200}); !v.OK {
		t.Fatalf("old slot should be free after reschedule, got %+v", v.Conflicts)
	}
	// 新位置正常阻塞。
	if v := s.Precheck(Candidate{ID: "c2", MachineID: "B", Start: 200, End: 300}); v.OK {
		t.Fatal("new slot should block same-machine candidates")
	}
}

func TestFailedRescheduleKeepsOld(t *testing.T) {
	s := NewScheduler()
	mustCommit(t, s, Candidate{ID: "r1", MachineID: "A", Start: 100, End: 200})
	mustCommit(t, s, Candidate{ID: "r2", MachineID: "B", Start: 300, End: 400})

	// 改期目标与 r2 冷却不足，必须失败且旧预约原样保留。
	v := s.Commit(Candidate{ID: "r1", MachineID: "B", Start: 350, End: 450})
	if v.OK {
		t.Fatal("reschedule conflicting with r2 should fail")
	}
	if v := s.Precheck(Candidate{ID: "c1", MachineID: "A", Start: 150, End: 250}); v.OK {
		t.Fatal("old reservation should still block its original slot")
	}
}

func TestExemptSkipsMachineButNotCapacity(t *testing.T) {
	s := NewScheduler()
	mustCommit(t, s, Candidate{ID: "r1", MachineID: "A", Start: 100, End: 200})

	// 豁免提交可与同机预约重叠。
	mustCommit(t, s, Candidate{ID: "x1", MachineID: "A", Start: 150, End: 250, Exempt: true})
	// 豁免登记的预约仍是后续普通提交必须避让的预约。
	v := s.Precheck(Candidate{ID: "c1", MachineID: "A", Start: 260, End: 300})
	if v.OK || !hasConflict(v, ConflictMachine) {
		t.Fatalf("exempt reservation should block later normal commits, got %+v", v)
	}

	// 豁免不能突破容量。
	s2 := NewScheduler()
	for i := 0; i < MaxConcurrentSessions; i++ {
		mustCommit(t, s2, Candidate{ID: fmt.Sprintf("b-%d", i), MachineID: fmt.Sprintf("M%d", i), Start: 0, End: 1000})
	}
	v = s2.Precheck(Candidate{ID: "x2", MachineID: "M0", Start: 100, End: 200, Exempt: true})
	if v.OK {
		t.Fatal("exempt commit must not bypass capacity")
	}
	if !hasConflict(v, ConflictCapacity) {
		t.Fatalf("expected capacity conflict, got %+v", v.Conflicts)
	}
	if hasConflict(v, ConflictMachine) {
		t.Fatalf("exempt mode must not report machine conflicts, got %+v", v.Conflicts)
	}
}

func TestBothConflictKindsReported(t *testing.T) {
	s := NewScheduler()
	for i := 0; i < MaxConcurrentSessions; i++ {
		mustCommit(t, s, Candidate{ID: fmt.Sprintf("b-%d", i), MachineID: fmt.Sprintf("M%d", i), Start: 0, End: 1000})
	}
	mustCommit(t, s, Candidate{ID: "peer", MachineID: "A", Start: 2000, End: 2100})
	v := s.Precheck(Candidate{ID: "c1", MachineID: "A", Start: 100, End: 2050})
	if v.OK {
		t.Fatal("expected rejection")
	}
	if !hasConflict(v, ConflictMachine) || !hasConflict(v, ConflictCapacity) {
		t.Fatalf("expected both machine and capacity conflicts, got %+v", v.Conflicts)
	}
}

func TestPrecheckDoesNotMutate(t *testing.T) {
	s := NewScheduler()
	mustCommit(t, s, Candidate{ID: "r1", MachineID: "A", Start: 100, End: 200})

	c := Candidate{ID: "c1", MachineID: "A", Start: 230, End: 300}
	for i := 0; i < 3; i++ {
		if v := s.Precheck(c); !v.OK {
			t.Fatalf("precheck %d should stay OK, got %+v", i, v.Conflicts)
		}
	}
	// 针对改期的预检同样不得改变后续判定。
	if v := s.Precheck(Candidate{ID: "r1", MachineID: "B", Start: 150, End: 250}); !v.OK {
		t.Fatalf("reschedule precheck should be OK, got %+v", v.Conflicts)
	}
	if v := s.Precheck(Candidate{ID: "c2", MachineID: "A", Start: 150, End: 250}); v.OK {
		t.Fatal("r1 should still occupy its original slot after reschedule precheck")
	}
}

func TestCancelReleases(t *testing.T) {
	s := NewScheduler()
	mustCommit(t, s, Candidate{ID: "r1", MachineID: "A", Start: 100, End: 200})
	if !s.Cancel("r1") {
		t.Fatal("Cancel should report existing reservation")
	}
	if s.Cancel("r1") {
		t.Fatal("Cancel of unknown id should return false")
	}
	if v := s.Precheck(Candidate{ID: "c1", MachineID: "A", Start: 100, End: 200}); !v.OK {
		t.Fatalf("slot should be free after cancel, got %+v", v.Conflicts)
	}
}
