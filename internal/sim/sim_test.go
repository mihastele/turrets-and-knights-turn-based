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
			s.towerShields[side][lane] = nil
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
	s.towerShields[SideB][0] = nil
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

func TestReinforcementWallBuildUpgradeBlockAndRebuild(t *testing.T) {
	s := newTestSim(t)
	s.anomalies = nil
	giveCoins(s, SideB, 500)
	giveCoins(s, SideA, 500)

	// Build a Wall for SideB in Lane 0
	if err := s.ApplyAction(Action{Kind: ActionBuildWall, Side: SideB, Lane: 0}); err != nil {
		t.Fatalf("build wall: %v", err)
	}
	w := s.walls[SideB][0]
	if w == nil || w.HP != s.cfg.Wall.MaxHP || w.Shield != s.cfg.Wall.BaseShield {
		t.Fatalf("unexpected wall state: %+v", w)
	}

	// Upgrade to Lv1 (thorns + extra HP/Shield)
	if err := s.ApplyAction(Action{Kind: ActionUpgradeWall, Side: SideB, Lane: 0}); err != nil {
		t.Fatalf("upgrade wall: %v", err)
	}
	if w.Level != 1 || w.MaxHP <= s.cfg.Wall.MaxHP || w.MaxShield <= s.cfg.Wall.BaseShield {
		t.Fatalf("wall upgrade did not boost stats: %+v", w)
	}

	// Send a SideA Soldier right in front of SideB's wall
	startCombat(t, s)
	sol := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: w.X - 1.0, HP: 75, MaxHP: 75, Damage: 10, TrackLevels: map[UpgradeTrack]int{}}
	s.units = []*Unit{sol}
	s.nextUnitID = 2

	hpBefore := w.HP + w.Shield
	solHPBefore := sol.HP
	step(s, 1.5)
	if w.HP+w.Shield >= hpBefore {
		t.Fatal("soldier should damage enemy wall")
	}
	if sol.HP >= solHPBefore {
		t.Fatal("upgraded wall thorns should damage melee attacker")
	}
	if sol.X > w.X {
		t.Fatalf("ground soldier walked past alive enemy wall: sol.X=%v w.X=%v", sol.X, w.X)
	}

	// Destroy the wall and verify it can be rebuilt in the next Prep phase
	w.Shield = 0
	w.HP = 1
	step(s, 1.5)
	if s.walls[SideB][0] != nil {
		t.Fatal("destroyed wall should be cleared")
	}
	step(s, s.cfg.Turn.CombatDuration)
	if s.Phase != PhasePrep {
		t.Fatalf("expected PhasePrep, got %s", s.Phase)
	}
	if err := s.ApplyAction(Action{Kind: ActionBuildWall, Side: SideB, Lane: 0}); err != nil {
		t.Fatalf("rebuild wall after destruction: %v", err)
	}
	if s.walls[SideB][0] == nil {
		t.Fatal("expected rebuilt wall in Lane 0")
	}
}

func TestHideLuckConcealsAnomaliesInPrepUntilCombat(t *testing.T) {
	s := newTestSim(t)
	s.SetHideLuck(true)
	if len(s.anomalies) == 0 {
		t.Fatal("expected round anomalies to spawn")
	}
	snapPrep := s.SnapshotFor(SideA)
	if !snapPrep.HideLuck {
		t.Fatal("expected snap.HideLuck = true")
	}
	if len(snapPrep.Anomalies) != 0 {
		t.Fatalf("expected 0 visible anomalies in Prep with HideLuck, got %d", len(snapPrep.Anomalies))
	}
	if snapPrep.HiddenLuckCount != len(s.anomalies) {
		t.Fatalf("HiddenLuckCount = %d, want %d", snapPrep.HiddenLuckCount, len(s.anomalies))
	}

	startCombat(t, s)
	snapCombat := s.SnapshotFor(SideA)
	if len(snapCombat.Anomalies) != len(s.anomalies) {
		t.Fatalf("expected all %d anomalies revealed in Combat, got %d", len(s.anomalies), len(snapCombat.Anomalies))
	}
}

func TestGoldLootContestedAndUncontested(t *testing.T) {
	// Case 1: Only one player has an army on the loot lane -> wins automatically
	s1 := newTestSim(t)
	clearTowers(s1)
	s1.anomalies = []*Anomaly{{ID: 100, Kind: "loot_coins", Lane: 1, X: 50, Radius: 3.2, Value: 35}}
	s1.units = []*Unit{{ID: 1, Type: Soldier, Side: SideA, Lane: 1, X: 10, HP: 75, MaxHP: 75, Damage: 10, TrackLevels: map[UpgradeTrack]int{}}}
	s1.nextUnitID = 2
	coinsA := coinBalance(s1, SideA)
	startCombat(t, s1)
	// End combat early before reaching X=50: SideA still wins uncontested gold loot
	s1.endCombatPhase()
	if coinBalance(s1, SideA) < coinsA+35 {
		t.Fatalf("uncontested side should win gold loot; coins=%d, want >=%d", coinBalance(s1, SideA), coinsA+35)
	}

	// Case 2: Both players have armies on the loot lane -> contested until one side wins the fight
	s2 := newTestSim(t)
	clearTowers(s2)
	s2.anomalies = []*Anomaly{{ID: 200, Kind: "loot_coins", Lane: 2, X: 50, Radius: 3.2, Value: 40}}
	solA := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 2, X: 49, HP: 200, MaxHP: 200, Damage: 25, TrackLevels: map[UpgradeTrack]int{}}
	solB := &Unit{ID: 2, Type: Soldier, Side: SideB, Lane: 2, X: 51, HP: 20, MaxHP: 75, Damage: 5, TrackLevels: map[UpgradeTrack]int{}}
	s2.units = []*Unit{solA, solB}
	s2.nextUnitID = 3
	startCombat(t, s2)
	if !s2.anomalies[0].Contested {
		t.Fatal("expected gold loot to be marked Contested when both sides are in lane")
	}
	coinsBeforeA := coinBalance(s2, SideA)
	step(s2, 1.2) // SideA kills SideB's soldier and wins the lane fight
	if len(s2.anomalies) != 0 {
		t.Fatal("expected contested gold loot to be claimed once SideA won the lane fight")
	}
	if coinBalance(s2, SideA) < coinsBeforeA+40 {
		t.Fatalf("expected SideA to receive +40c loot after winning lane fight, got +%d", coinBalance(s2, SideA)-coinsBeforeA)
	}
}

func TestMudPileVariableSizeAndSpikeFieldDoT(t *testing.T) {
	s := newTestSim(t)
	clearTowers(s)
	s.anomalies = []*Anomaly{
		{ID: 1, Kind: "hazard_mud", Lane: 0, X: 50, Radius: 8.0},
		{ID: 2, Kind: "hazard_spikes", Lane: 1, X: 50, Radius: 6.0, TickIn: 0.1},
	}
	if !s.inMudHazard(0, 43.0) || s.inMudHazard(0, 41.0) {
		t.Fatal("inMudHazard should respect variable radius 8.0 around X=50")
	}

	startCombat(t, s)
	ground := &Unit{ID: 10, Type: Soldier, Side: SideA, Lane: 1, X: 48, HP: 75, MaxHP: 75, Damage: 10, TrackLevels: map[UpgradeTrack]int{}}
	flyer := &Unit{ID: 11, Type: Dragon, Side: SideA, Lane: 1, X: 48, HP: 240, MaxHP: 240, Damage: 26, TrackLevels: map[UpgradeTrack]int{}}
	s.units = []*Unit{ground, flyer}
	s.nextUnitID = 12

	step(s, 0.5)
	if ground.HP >= 75 {
		t.Fatalf("spike field should damage ground unit over time, hp=%v", ground.HP)
	}
	if flyer.HP != 240 {
		t.Fatalf("spike field should not damage flying unit, hp=%v", flyer.HP)
	}
}

func TestMidfieldPillageAndScoutLane(t *testing.T) {
	s := newTestSim(t)
	s.SetHideLuck(true)
	clearTowers(s)
	s.anomalies = []*Anomaly{{ID: 1, Kind: "loot_buff", Lane: 3, X: 50, Value: 35}}

	// Scout Lane 3 during Prep -> reveals hidden anomaly in Lane 3
	if err := s.ApplyAction(Action{Kind: ActionScoutLane, Side: SideA, Lane: 3}); err != nil {
		t.Fatalf("scout lane: %v", err)
	}
	snap := s.SnapshotFor(SideA)
	if len(snap.Anomalies) != 1 || snap.Anomalies[0].Lane != 3 {
		t.Fatalf("expected scouted lane 3 anomaly to be visible, got %+v", snap.Anomalies)
	}

	// Unit surviving past X=50 earns Midfield Pillage bonus at endCombatPhase
	s.anomalies = nil
	s.units = []*Unit{{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: 65, HP: 75, MaxHP: 75, Damage: 10, TrackLevels: map[UpgradeTrack]int{}}}
	s.nextUnitID = 2
	startCombat(t, s)
	coinsBefore := coinBalance(s, SideA)
	s.endCombatPhase()
	expectedMin := coinsBefore + s.cfg.Turn.PillageBonus
	if coinBalance(s, SideA) < expectedMin {
		t.Fatalf("expected pillage bonus in coins; got %d, want >=%d", coinBalance(s, SideA), expectedMin)
	}
	if s.laneDominance[0] != SideA {
		t.Fatalf("expected SideA to hold lane 0 dominance, got %d", s.laneDominance[0])
	}
}

func TestMaxRoundsAndScalingCombatDuration(t *testing.T) {
	s := newTestSim(t)
	if s.cfg.Turn.MaxRounds != 30 {
		t.Fatalf("MaxRounds = %d, want 30", s.cfg.Turn.MaxRounds)
	}
	if s.cfg.Turn.CombatDuration != 27 {
		t.Fatalf("CombatDuration = %v, want 27", s.cfg.Turn.CombatDuration)
	}
	if s.cfg.Turn.CombatDurationStep != 1 {
		t.Fatalf("CombatDurationStep = %v, want 1", s.cfg.Turn.CombatDurationStep)
	}

	keepAlive := func() {
		s.anomalies = nil
		s.units = []*Unit{
			{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: 48, HP: 10000, MaxHP: 10000, Damage: 0, TrackLevels: map[UpgradeTrack]int{}},
			{ID: 2, Type: Shieldbearer, Side: SideB, Lane: 0, X: 52, HP: 10000, MaxHP: 10000, Damage: 0, TrackLevels: map[UpgradeTrack]int{}},
		}
		s.nextUnitID = 3
	}

	// Round 1 combat lasts 27s
	keepAlive()
	startCombat(t, s)
	if s.PhaseRemaining != 27 {
		t.Fatalf("Round 1 PhaseRemaining = %v, want 27", s.PhaseRemaining)
	}
	step(s, 26.5)
	if s.Phase != PhaseCombat || s.Round != 1 {
		t.Fatalf("expected Round 1 Combat to still be active at 26.5s, got R%d %s", s.Round, s.Phase)
	}
	step(s, 0.7)
	if s.Phase != PhasePrep || s.Round != 2 {
		t.Fatalf("expected Round 2 Prep after 27s combat, got R%d %s", s.Round, s.Phase)
	}

	// Round 2 combat lasts 28s
	keepAlive()
	startCombat(t, s)
	if s.PhaseRemaining != 28 {
		t.Fatalf("Round 2 PhaseRemaining = %v, want 28", s.PhaseRemaining)
	}
	step(s, 27.5)
	if s.Phase != PhaseCombat || s.Round != 2 {
		t.Fatalf("expected Round 2 Combat to still be active at 27.5s, got R%d %s", s.Round, s.Phase)
	}
	step(s, 0.7)
	if s.Phase != PhasePrep || s.Round != 3 {
		t.Fatalf("expected Round 3 Prep after 28s combat, got R%d %s", s.Round, s.Phase)
	}

	// Round 3 combat lasts 29s
	keepAlive()
	startCombat(t, s)
	if s.PhaseRemaining != 29 {
		t.Fatalf("Round 3 PhaseRemaining = %v, want 29", s.PhaseRemaining)
	}
}

func TestInitialTowerShieldInFrontOfTower(t *testing.T) {
	s := newTestSim(t)
	s.anomalies = nil

	// Every lane starts with a weaker shield just in front of each tower
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			ts := s.towerShields[side][lane]
			tr := s.turrets[side][lane][0]
			if ts == nil || tr == nil {
				t.Fatalf("side %d lane %d missing tower shield or tower", side, lane)
			}
			if side == SideA && ts.X <= tr.X {
				t.Fatalf("SideA shield X=%v should be in front of tower X=%v", ts.X, tr.X)
			}
			if side == SideB && ts.X >= tr.X {
				t.Fatalf("SideB shield X=%v should be in front of tower X=%v", ts.X, tr.X)
			}
			if ts.MaxHP+ts.MaxShield >= s.cfg.Wall.MaxHP+s.cfg.Wall.BaseShield {
				t.Fatalf("TowerShield (%v) should be weaker than Reinforcement Wall (%v)", ts.MaxHP+ts.MaxShield, s.cfg.Wall.MaxHP+s.cfg.Wall.BaseShield)
			}
		}
	}

	snap := s.SnapshotFor(SideA)
	if len(snap.TowerShields) != 2*NumLanes {
		t.Fatalf("expected %d tower shields in snapshot, got %d", 2*NumLanes, len(snap.TowerShields))
	}

	// Ground unit approaches SideB's lane 0: blocked by TowerShield before reaching Tower
	startCombat(t, s)
	sol := &Unit{ID: 1, Type: Soldier, Side: SideA, Lane: 0, X: 83, HP: 500, MaxHP: 500, Damage: 20, TrackLevels: map[UpgradeTrack]int{}}
	s.units = []*Unit{sol}
	s.nextUnitID = 2

	ts := s.towerShields[SideB][0]
	tr := s.turrets[SideB][0][0]
	trBefore := tr.HP + tr.Shield
	step(s, 2.5)
	if ts.HP+ts.Shield >= ts.MaxHP+ts.MaxShield {
		t.Fatal("soldier should damage enemy TowerShield before tower")
	}
	if tr.HP+tr.Shield != trBefore {
		t.Fatalf("tower should be protected while TowerShield stands; before=%v after=%v", trBefore, tr.HP+tr.Shield)
	}
	if sol.X > ts.X {
		t.Fatalf("ground soldier walked past alive TowerShield: sol.X=%v ts.X=%v", sol.X, ts.X)
	}
}
