package sim

// Snapshot is the state broadcast to clients. It contains everything a
// player is allowed to see for the current turn phase.
type Snapshot struct {
	Type           string            `json:"type"` // always "snapshot"
	Tick           int64             `json:"tick"`
	Time           float64           `json:"time"`
	Phase          string            `json:"phase"` // "prep" | "combat"
	Round          int               `json:"round"`
	MaxRounds      int               `json:"maxRounds"`
	PhaseRemaining float64           `json:"phaseRemaining"`
	Ready          [2]bool           `json:"ready"`
	Overtime       bool              `json:"overtime"`
	TurretFactor   float64           `json:"turretFactor"`
	Finished       bool              `json:"finished"`
	Draw           bool              `json:"draw"`
	Winner         int               `json:"winner"`  // NoSide when undecided or draw
	WinLane        int               `json:"winLane"` // -1 when undecided
	TimeLimit      float64           `json:"timeLimit"`
	OvertimeStart  float64           `json:"overtimeStart"`
	Players        [2]PlayerSnapshot `json:"players"`
	Units          []UnitSnapshot    `json:"units"`
	Turrets        []TurretSnapshot      `json:"turrets"`
	TowerShields   []TowerShieldSnapshot `json:"towerShields"`
	Walls          []WallSnapshot        `json:"walls"`
	Caches         []CacheSnapshot   `json:"caches"`
	Anomalies      []AnomalySnapshot `json:"anomalies"`
	Burns          []BurnSnapshot    `json:"burns"`
	HideLuck        bool              `json:"hideLuck"`
	HiddenLuckCount int               `json:"hiddenLuckCount"`
	WinMode         string            `json:"winMode"`
	BreachesToWin   int               `json:"breachesToWin"`
	MaxBreachLoots  int               `json:"maxBreachLoots"`
	LaneDominance   [NumLanes]int     `json:"laneDominance"`
	ScoutedLanes   [NumLanes]bool    `json:"scoutedLanes"`
	Events         []Event           `json:"events"`
	Fx             []FxEvent         `json:"fx"`
}

type PlayerSnapshot struct {
	Coins             int                               `json:"coins"`
	Income            int                               `json:"income"` // estimated next-round base + factory income
	Factory           bool                              `json:"factory"`
	FactoryLevel      int                               `json:"factoryLevel"`
	Lives              int                               `json:"lives"`
	RoundBreaches      int                               `json:"roundBreaches"`
	RoundBreachedLanes [NumLanes]bool                    `json:"roundBreachedLanes"`
	UnitUpgrades       map[UnitType]int                  `json:"unitUpgrades"`
	UnitTrackUpgrades map[UnitType]map[UpgradeTrack]int `json:"unitTrackUpgrades"`
	SoldierPool       map[UnitType]int                  `json:"soldierPool"`
	DamageDealt       int                               `json:"damageDealt"`
	Kills             int                               `json:"kills"`
	UnitsLost         int                               `json:"unitsLost"`
	TurretsDown       int                               `json:"turretsDown"`
	BountyEarned      int                               `json:"bountyEarned"`
	Abilities         map[string]float64                `json:"abilities"` // coupon field-effect id -> seconds until ready (0 = ready)
}

// CacheSnapshot describes a supply cache currently on the field.
type CacheSnapshot struct {
	Lane      int     `json:"lane"`
	X         float64 `json:"x"`
	Amount    int     `json:"amount"`
	ExpiresIn float64 `json:"expiresIn"`
}

// AnomalySnapshot describes a random lane loot, shrine, neutral monster, or hazard.
type AnomalySnapshot struct {
	ID        int64   `json:"id"`
	Kind      string  `json:"kind"` // "loot_coins" | "loot_buff" | "neutral_golem" | "hazard_fire" | "hazard_tar" | "hazard_mud" | "hazard_spikes"
	Lane      int     `json:"lane"`
	X         float64 `json:"x"`
	Radius    float64 `json:"radius,omitempty"`
	Contested bool    `json:"contested,omitempty"`
	HP        float64 `json:"hp,omitempty"`
	MaxHP     float64 `json:"maxHp,omitempty"`
	Value     int     `json:"value,omitempty"`
}

// TowerShieldSnapshot describes a pre-built shield standing in front of a tower.
type TowerShieldSnapshot struct {
	Side      int     `json:"side"`
	Lane      int     `json:"lane"`
	X         float64 `json:"x"`
	HP        float64 `json:"hp"`
	MaxHP     float64 `json:"maxHp"`
	Shield    float64 `json:"shield"`
	MaxShield float64 `json:"maxShield"`
}

// WallSnapshot describes a lane reinforcement wall.
type WallSnapshot struct {
	Side      int     `json:"side"`
	Lane      int     `json:"lane"`
	X         float64 `json:"x"`
	Level     int     `json:"level"`
	HP        float64 `json:"hp"`
	MaxHP     float64 `json:"maxHp"`
	Shield    float64 `json:"shield"`
	MaxShield float64 `json:"maxShield"`
}

// BurnSnapshot describes an active Dragon Fire field — a lane left ablaze.
type BurnSnapshot struct {
	Lane      int     `json:"lane"`
	Side      int     `json:"side"`      // who cast it
	Remaining float64 `json:"remaining"` // seconds left
}

type UnitSnapshot struct {
	ID        int64    `json:"id"`
	Type      UnitType `json:"type"`
	Side      int      `json:"side"`
	Lane      int      `json:"lane"`
	X         float64  `json:"x"`
	YOff      float64  `json:"yOff"`
	HP        float64  `json:"hp"`
	MaxHP     float64  `json:"maxHp"`
	Shield    float64  `json:"shield"`
	MaxShield float64  `json:"maxShield"`
	Staged    bool     `json:"staged,omitempty"`
	Buffed    bool     `json:"buffed,omitempty"`
}

type TurretSnapshot struct {
	Side        int                  `json:"side"`
	Lane        int                  `json:"lane"`
	Slot        int                  `json:"slot"`
	Level       int                  `json:"level"`
	HP          float64              `json:"hp"`
	MaxHP       float64              `json:"maxHp"`
	Shield      float64              `json:"shield"`
	MaxShield   float64              `json:"maxShield"`
	Damage      float64              `json:"damage"`
	TrackLevels map[UpgradeTrack]int `json:"trackLevels,omitempty"`
}

// Snapshot builds the full (unfiltered) wire representation of the current state.
// Events and Fx are drained and included once.
func (s *Sim) Snapshot() Snapshot {
	return s.buildSnapshot(SideNone, true)
}

// SnapshotFor builds a per-viewer snapshot that enforces Fog of War during
// PhasePrep (hiding the opponent's staged lane units until Combat begins).
// Callers broadcasting to multiple players should call DrainEvents/DrainFx once
// or use SnapshotFor with drain=false after Snapshot(), or use SnapshotPair().
func (s *Sim) SnapshotFor(viewerSide int) Snapshot {
	return s.buildSnapshot(viewerSide, false)
}

// SnapshotPair returns fog-of-war-filtered snapshots for SideA and SideB,
// draining events and FX once and sharing them across both snapshots.
func (s *Sim) SnapshotPair() [2]Snapshot {
	events := emptyIfNil(s.DrainEvents())
	fx := emptyIfNil(s.DrainFx())
	snapA := s.buildSnapshot(SideA, false)
	snapB := s.buildSnapshot(SideB, false)
	snapA.Events = events
	snapA.Fx = fx
	snapB.Events = events
	snapB.Fx = fx
	return [2]Snapshot{snapA, snapB}
}

func (s *Sim) buildSnapshot(viewerSide int, drain bool) Snapshot {
	var events []Event
	var fx []FxEvent
	if drain {
		events = emptyIfNil(s.DrainEvents())
		fx = emptyIfNil(s.DrainFx())
	} else {
		events = []Event{}
		fx = []FxEvent{}
	}

	snap := Snapshot{
		Type:           "snapshot",
		Tick:           s.Tick,
		Time:           s.Time,
		Phase:          string(s.Phase),
		Round:          s.Round,
		MaxRounds:      s.cfg.Turn.MaxRounds,
		PhaseRemaining: s.PhaseRemaining,
		Ready:          s.Ready,
		Overtime:       s.Overtime,
		TurretFactor:   s.TurretFactor,
		Finished:       s.Finished,
		Draw:           s.Draw,
		Winner:         s.Winner,
		WinLane:        s.WinLane,
		TimeLimit:      float64(s.cfg.Turn.MaxRounds),
		OvertimeStart:  float64(s.cfg.Turn.EscalationStartRound),
		HideLuck:       s.HideLuck,
		WinMode:        string(s.WinMode),
		BreachesToWin:  s.BreachesToWin,
		MaxBreachLoots: s.MaxBreachLoots,
		LaneDominance:  s.laneDominance,
		Events:         events,
		Fx:             fx,
	}
	if viewerSide == SideA || viewerSide == SideB {
		snap.ScoutedLanes = s.scoutedLanes[viewerSide]
	}

	hideEnemyPrep := s.Phase == PhasePrep && (viewerSide == SideA || viewerSide == SideB)

	for i := 0; i < 2; i++ {
		p := &s.players[i]
		ps := PlayerSnapshot{
			Coins:             p.Coins,
			Income:            p.EstimatedRoundIncome(s.cfg, s.Round+1),
			Factory:           p.Factory,
			FactoryLevel:      p.FactoryLevel,
			Lives:              p.Lives,
			RoundBreaches:      s.countRoundBreaches(i),
			RoundBreachedLanes: s.roundBreachedLanes[i],
			UnitUpgrades:       copyUpgrades(p.UnitUpgrades),
			UnitTrackUpgrades: copyUnitTrackUpgrades(p.UnitTrackUpgrades),
			SoldierPool:       copyPool(p.SoldierPool),
			DamageDealt:       int(p.DamageDealt + 0.5),
			Kills:             p.Kills,
			UnitsLost:         p.UnitsLost,
			TurretsDown:       p.TurretsDown,
			BountyEarned:      p.BountyEarned,
			Abilities:         abilityCooldowns(s.abilityReady[i], s.Time),
		}
		if hideEnemyPrep && i != viewerSide {
			// Fog of war: mask opponent's pool during prep so they can bluff
			ps.SoldierPool = map[UnitType]int{}
		}
		snap.Players[i] = ps
	}

	snap.Units = make([]UnitSnapshot, 0, len(s.units))
	for _, u := range s.units {
		if hideEnemyPrep && u.Side != viewerSide && !(validLane(u.Lane) && s.scoutedLanes[viewerSide][u.Lane]) {
			continue
		}
		snap.Units = append(snap.Units, UnitSnapshot{
			ID: u.ID, Type: u.Type, Side: u.Side, Lane: u.Lane,
			X: u.X, YOff: u.YOff, HP: u.HP, MaxHP: u.MaxHP,
			Shield: u.Shield, MaxShield: u.MaxShield,
			Staged: u.Staged, Buffed: u.SpeedBuff > 0,
		})
	}

	snap.Turrets = make([]TurretSnapshot, 0, 2*NumLanes*TurretSlots)
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < TurretSlots; slot++ {
				if t := s.turrets[side][lane][slot]; t != nil {
					snap.Turrets = append(snap.Turrets, TurretSnapshot{
						Side: t.Side, Lane: t.Lane, Slot: t.Slot, Level: t.Level,
						HP: t.HP, MaxHP: t.MaxHP, Shield: t.Shield, MaxShield: t.MaxShield,
						Damage: t.Damage, TrackLevels: copyTrackMap(t.TrackLevels),
					})
				}
			}
		}
	}

	snap.TowerShields = make([]TowerShieldSnapshot, 0, 2*NumLanes)
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			if ts := s.towerShields[side][lane]; ts != nil {
				snap.TowerShields = append(snap.TowerShields, TowerShieldSnapshot{
					Side: ts.Side, Lane: ts.Lane, X: ts.X,
					HP: ts.HP, MaxHP: ts.MaxHP, Shield: ts.Shield, MaxShield: ts.MaxShield,
				})
			}
		}
	}

	snap.Walls = make([]WallSnapshot, 0, 2*NumLanes)
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			if w := s.walls[side][lane]; w != nil {
				snap.Walls = append(snap.Walls, WallSnapshot{
					Side: w.Side, Lane: w.Lane, X: w.X, Level: w.Level,
					HP: w.HP, MaxHP: w.MaxHP, Shield: w.Shield, MaxShield: w.MaxShield,
				})
			}
		}
	}

	snap.Caches = make([]CacheSnapshot, 0, len(s.caches))
	for _, c := range s.caches {
		snap.Caches = append(snap.Caches, CacheSnapshot{
			Lane: c.Lane, X: c.X, Amount: c.Amount, ExpiresIn: c.ExpiresAt - s.Time,
		})
	}

	snap.Anomalies = make([]AnomalySnapshot, 0, len(s.anomalies))
	for _, a := range s.anomalies {
		if hideEnemyPrep && s.HideLuck && !(validLane(a.Lane) && s.scoutedLanes[viewerSide][a.Lane]) {
			snap.HiddenLuckCount++
			continue
		}
		snap.Anomalies = append(snap.Anomalies, AnomalySnapshot{
			ID: a.ID, Kind: a.Kind, Lane: a.Lane, X: a.X,
			Radius: a.Radius, Contested: a.Contested,
			HP: a.HP, MaxHP: a.MaxHP, Value: a.Value,
		})
	}

	snap.Burns = make([]BurnSnapshot, 0, len(s.burns))
	for _, b := range s.burns {
		snap.Burns = append(snap.Burns, BurnSnapshot{
			Lane: b.Lane, Side: b.Side, Remaining: b.Until - s.Time,
		})
	}
	return snap
}

func emptyIfNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func abilityCooldowns(readyAt map[string]float64, now float64) map[string]float64 {
	out := make(map[string]float64, len(readyAt))
	for id, t := range readyAt {
		rem := t - now
		if rem < 0 {
			rem = 0
		}
		out[id] = rem
	}
	return out
}

func copyUpgrades(in map[UnitType]int) map[UnitType]int {
	out := make(map[UnitType]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyPool(in map[UnitType]int) map[UnitType]int {
	out := make(map[UnitType]int, len(in))
	for k, v := range in {
		if v > 0 {
			out[k] = v
		}
	}
	return out
}

func copyTrackMap(in map[UpgradeTrack]int) map[UpgradeTrack]int {
	out := make(map[UpgradeTrack]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyUnitTrackUpgrades(in map[UnitType]map[UpgradeTrack]int) map[UnitType]map[UpgradeTrack]int {
	out := make(map[UnitType]map[UpgradeTrack]int, len(in))
	for u, tracks := range in {
		out[u] = copyTrackMap(tracks)
	}
	return out
}
