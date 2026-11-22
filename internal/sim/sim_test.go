package sim

import (
	"errors"
	"math"
	"testing"
)

func newTestSim(t *testing.T) *Sim {
	t.Helper()
	return New(42, DefaultBalance())
}

func giveCoins(s *Sim, side, amount int) {
	s.players[side].Coins = amount
}

func coinBalance(s *Sim, side int) int { return s.players[side].Coins }

func startCombat(t *testing.T, s *Sim) {
	t.Helper()
	if s.Phase == PhaseCombat {
		return
	}
	if !s.Ready[SideA] {
		if err := s.ApplyAction(Action{Kind: ActionReady, Side: SideA}); err != nil {
			t.Fatalf("ready A: %v", err)
		}
	}
	if s.Phase != PhaseCombat && !s.Ready[SideB] {
		if err := s.ApplyAction(Action{Kind: ActionReady, Side: SideB}); err != nil {
			t.Fatalf("ready B: %v", err)
		}
	}
	if s.Phase != PhaseCombat {
		t.Fatalf("expected PhaseCombat, got %s", s.Phase)
	}
}

func clearTowers(s *Sim) {
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < TurretSlots; slot++ {
				s.turrets[side][lane][slot] = nil
			}
		}
	}
}

func step(s *Sim, seconds float64) {
	ticks := int(math.Round(seconds / TickDT))
	for i := 0; i < ticks; i++ {
		s.Step(TickDT)
	}
}

func TestStartStateAndInitialTowers(t *testing.T) {
	s := newTestSim(t)
	if s.Phase != PhasePrep || s.Round != 1 {
		t.Fatalf("expected Round 1 PhasePrep, got R%d %s", s.Round, s.Phase)
	}
	if got := coinBalance(s, SideA); got != s.cfg.StartCoins {
		t.Fatalf("side A starts with %d coins, want %d", got, s.cfg.StartCoins)
	}
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			if s.turrets[side][lane][0] == nil {
				t.Fatalf("side %d lane %d missing initial protective tower", side, lane)
			}
			if s.turrets[side][lane][1] != nil {
				t.Fatalf("side %d lane %d slot 1 should start empty", side, lane)
			}
		}
	}
}

func TestPrepReadyStartsCombatImmediately(t *testing.T) {
	s := newTestSim(t)
	if err := s.ApplyAction(Action{Kind: ActionReady, Side: SideA}); err != nil {
		t.Fatal(err)
	}
	if s.Phase != PhasePrep {
		t.Fatal("combat started with only 1 player ready")
	}
	if err := s.ApplyAction(Action{Kind: ActionReady, Side: SideB}); err != nil {
		t.Fatal(err)
	}
	if s.Phase != PhaseCombat {
		t.Fatalf("expected PhaseCombat when both ready, got %s", s.Phase)
	}
}

func TestPrepTimerStartsCombatAutomatically(t *testing.T) {
	s := newTestSim(t)
	step(s, s.cfg.Turn.PrepDuration+0.1)
	if s.Phase != PhaseCombat {
		t.Fatalf("expected PhaseCombat after prep timer, got %s", s.Phase)
	}
}

func TestFogOfWarHidesEnemyStagedUnitsInPrep(t *testing.T) {
	s := newTestSim(t)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideB, Lane: 2, Unit: Knight}); err != nil {
		t.Fatal(err)
	}
	snapA := s.SnapshotFor(SideA)
	for _, u := range snapA.Units {
		if u.Side != SideA {
			t.Fatalf("SideA saw enemy unit %+v during Prep Fog of War", u)
		}
	}
	if len(snapA.Units) != 1 {
		t.Fatalf("SideA should see 1 own unit, got %d", len(snapA.Units))
	}
	startCombat(t, s)
	snapAfter := s.SnapshotFor(SideA)
	if len(snapAfter.Units) != 2 {
		t.Fatalf("expected both units visible in Combat, got %d", len(snapAfter.Units))
	}
}

func TestSoldierPoolReturnAndFreeRedeploy(t *testing.T) {
	s := newTestSim(t)
	s.cfg.Turn.CombatDuration = 10
	s.anomalies = nil
	giveCoins(s, SideA, 100)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	if coinBalance(s, SideA) != 80 {
		t.Fatalf("coins = %d, want 80", coinBalance(s, SideA))
	}
	startCombat(t, s)
	// Fast-forward 10s combat round: soldier reaches X=55 (alive, before tower range)
	step(s, s.cfg.Turn.CombatDuration+0.1)
	if s.Phase != PhasePrep || s.Round != 2 {
		t.Fatalf("expected Round 2 Prep, got R%d %s", s.Round, s.Phase)
	}
	if got := s.players[SideA].SoldierPool[Soldier]; got != 1 {
		t.Fatalf("surviving soldier should return to pool, got %d", got)
	}
	coinsBefore := coinBalance(s, SideA)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 3, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	if coinBalance(s, SideA) != coinsBefore {
		t.Fatal("deploying from SoldierPool should cost 0 coins")
	}
	if s.players[SideA].SoldierPool[Soldier] != 0 {
		t.Fatal("SoldierPool should decrement after re-deploy")
	}
}

func TestRecallUnitReturnsToPoolInPrep(t *testing.T) {
	s := newTestSim(t)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 1, Unit: Bowman}); err != nil {
		t.Fatal(err)
	}
	if err := s.ApplyAction(Action{Kind: ActionRecallUnit, Side: SideA, Lane: 1}); err != nil {
		t.Fatal(err)
	}
	if s.players[SideA].SoldierPool[Bowman] != 1 {
		t.Fatalf("expected recalled bowman in pool, got %d", s.players[SideA].SoldierPool[Bowman])
	}
	if len(s.units) != 0 {
		t.Fatalf("expected 0 staged units after recall, got %d", len(s.units))
	}
}

func TestDeployLockedInCombat(t *testing.T) {
	s := newTestSim(t)
	startCombat(t, s)
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); !errors.Is(err, ErrWrongPhase) {
		t.Fatalf("want ErrWrongPhase in combat, got %v", err)
	}
}

func TestFiveTrackUpgradesAndSpecialCouponLock(t *testing.T) {
	s := newTestSim(t)
	giveCoins(s, SideA, 1000)
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier, Track: TrackAttackSpeed}); err != nil {
		t.Fatalf("upgrade attackSpeed: %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier, Track: TrackShield}); err != nil {
		t.Fatalf("upgrade shield: %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier, Track: TrackSpecial}); !errors.Is(err, ErrCouponRequired) {
		t.Fatalf("special track without coupon: want ErrCouponRequired, got %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionUpgradeUnit, Side: SideA, Unit: Soldier, Track: TrackSpecial, Entitled: true}); err != nil {
		t.Fatalf("special track with coupon: %v", err)
	}
	if err := s.ApplyAction(Action{Kind: ActionDeploy, Side: SideA, Lane: 0, Unit: Soldier}); err != nil {
		t.Fatal(err)
	}
	u := s.units[0]
	if u.Shield <= 0 {
		t.Fatalf("expected upgraded soldier to have shield, got %v", u.Shield)
	}
}

func TestCounterDamageMatrixAndKillBounty(t *testing.T) {
	s := newTestSim(t)
	clearTowers(s)
	s.anomalies = nil
	startCombat(t, s)

	// Soldier (Infantry) vs Knight (Cavalry) -> Soldier has 1.45x multiplier!
	sol := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: 50, HP: 75, MaxHP: 75, Damage: 10, TrackLevels: map[UpgradeTrack]int{}}
	kni := &Unit{ID: 2, Type: Knight, Side: SideB, Lane: 0, X: 51, HP: 10, MaxHP: 100, Damage: 16, TrackLevels: map[UpgradeTrack]int{}}
	s.units = []*Unit{sol, kni}
	s.nextUnitID = 3

	coinsBefore := coinBalance(s, SideA)
	step(s, 0.2) // Soldier hits Knight for 14.5 dmg, killing it
	if kni.HP > 0 {
		t.Fatalf("expected soldier to kill 10-HP knight, remaining hp=%v", kni.HP)
	}
	gained := coinBalance(s, SideA) - coinsBefore
	wantBounty := int(math.Round(float64(s.cfg.Units[Knight].Cost) * 0.35))
	if gained != wantBounty {
		t.Fatalf("kill bounty = %d, want %d", gained, wantBounty)
	}
}

func TestTowerBlocksGroundUnitAndAllUnitsAttackTowers(t *testing.T) {
	s := newTestSim(t)
	s.anomalies = nil
	startCombat(t, s)

	// Place a Soldier right in front of SideB's initial tower at X=90
	sol := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: 88, HP: 500, MaxHP: 500, Damage: 20, TrackLevels: map[UpgradeTrack]int{}}
	s.units = []*Unit{sol}
	s.nextUnitID = 2

	tr := s.turrets[SideB][0][0]
	hpBefore := tr.HP + tr.Shield
	step(s, 2.5)
	if tr.HP+tr.Shield >= hpBefore {
		t.Fatal("soldier should damage enemy protective tower")
	}
	if sol.X > tr.X {
		t.Fatalf("ground soldier walked past alive enemy tower: sol.X=%v tr.X=%v", sol.X, tr.X)
	}
}

func TestNeutralGolemFightsAndAwardsBounty(t *testing.T) {
	s := newTestSim(t)
	clearTowers(s)
	s.anomalies = []*Anomaly{{
		ID: 500, Kind: "neutral_golem", Lane: 1, X: 50, HP: 15, MaxHP: 160, Damage: 15, Value: 50, LastHitBy: -1,
	}}
	startCombat(t, s)

	sol := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 1, X: 49, HP: 75, MaxHP: 75, Damage: 20, TrackLevels: map[UpgradeTrack]int{}}
	s.units = []*Unit{sol}
	s.nextUnitID = 2

	coinsBefore := coinBalance(s, SideA)
	step(s, 1.0)
	if len(s.anomalies) != 0 {
		t.Fatal("expected neutral golem to be slain and removed")
	}
	if got := coinBalance(s, SideA) - coinsBefore; got != 50 {
		t.Fatalf("golem bounty = %d, want 50", got)
	}
}

func TestBreachWinsWhenLaneTowerDestroyed(t *testing.T) {
	s := newTestSim(t)
	clearTowers(s)
	s.anomalies = nil
	startCombat(t, s)

	s.units = []*Unit{{
		ID: 1, Type: Knight, Side: SideA, Lane: 2, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16, TrackLevels: map[UpgradeTrack]int{},
	}}
	step(s, 0.5)
	if !s.Finished || s.Winner != SideA || s.WinLane != 2 {
		t.Fatalf("expected SideA breach in lane 2, got finished=%v winner=%d lane=%d", s.Finished, s.Winner, s.WinLane)
	}
}

func TestBreachAwardsGoldToCrossingSide(t *testing.T) {
	s := newTestSim(t)
	s.SetLives(3)
	clearTowers(s)
	s.anomalies = nil
	startCombat(t, s)

	coinsABefore := coinBalance(s, SideA)
	coinsBBefore := coinBalance(s, SideB)

	s.units = []*Unit{
		{ID: 1, Type: Knight, Side: SideA, Lane: 1, X: FieldLength - 0.5, HP: 100, MaxHP: 100, Damage: 16, TrackLevels: map[UpgradeTrack]int{}},
		{ID: 2, Type: Knight, Side: SideB, Lane: 3, X: 0.5, HP: 100, MaxHP: 100, Damage: 16, TrackLevels: map[UpgradeTrack]int{}},
	}
	step(s, 0.5)

	if s.Finished {
		t.Fatalf("expected match to continue with 2 lives remaining, got finished=true")
	}
	wantA := s.cfg.Turn.BreachBounty + s.cfg.Turn.BreachComebackCoins
	wantB := s.cfg.Turn.BreachBounty + s.cfg.Turn.BreachComebackCoins
	if got := coinBalance(s, SideA) - coinsABefore; got != wantA {
		t.Fatalf("SideA coin delta = %d, want %d", got, wantA)
	}
	if got := coinBalance(s, SideB) - coinsBBefore; got != wantB {
		t.Fatalf("SideB coin delta = %d, want %d", got, wantB)
	}
	if s.players[SideA].BountyEarned != s.cfg.Turn.BreachBounty {
		t.Fatalf("SideA BountyEarned = %d, want %d", s.players[SideA].BountyEarned, s.cfg.Turn.BreachBounty)
	}
	if s.players[SideB].BountyEarned != s.cfg.Turn.BreachBounty {
		t.Fatalf("SideB BountyEarned = %d, want %d", s.players[SideB].BountyEarned, s.cfg.Turn.BreachBounty)
	}
}

func TestResignEndsMatch(t *testing.T) {
	s := newTestSim(t)
	s.Resign(SideB)
	if !s.Finished || s.Winner != SideA || s.Draw {
		t.Fatalf("resign failed: finished=%v winner=%d draw=%v", s.Finished, s.Winner, s.Draw)
	}
}
