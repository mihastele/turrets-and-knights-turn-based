package sim

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
)

// Side indices.
const (
	SideNone = -1
)

// TurnPhase represents the current phase of a turn-based round.
type TurnPhase string

const (
	PhasePrep   TurnPhase = "prep"
	PhaseCombat TurnPhase = "combat"
)

// ---------------------------------------------------------------------------
// Actions (client intentions). The server validates everything; the client
// never reports money, damage, positions or victory.
// ---------------------------------------------------------------------------

type ActionKind int

const (
	ActionDeploy ActionKind = iota
	ActionBuildTurret
	ActionBuildFactory
	ActionUpgradeUnit
	ActionUpgradeTurret
	ActionFieldEffect
	ActionUpgradeFactory
	ActionReady
	ActionRecallUnit
)

// Coupon field-effect identifiers (balance lives in Balance.Coupon).
const (
	EffectRockslide  = "rockslide"
	EffectDragonFire = "dragonfire"
	EffectWarcry     = "warcry"
)

// Action is a player intention.
type Action struct {
	Kind     ActionKind
	Side     int // SideA or SideB, filled in by the server from the connection
	Lane     int
	Unit     UnitType
	Slot     int
	Track    UpgradeTrack // upgrade track for ActionUpgradeUnit / ActionUpgradeTurret
	Effect   string       // field-effect id for ActionFieldEffect
	Entitled bool         // set by the server from the coupon store; never trusted from clients
}

var (
	ErrMatchOver      = errors.New("match is over")
	ErrInvalidLane    = errors.New("invalid lane")
	ErrNotEnoughCoins = errors.New("not enough coins")
	ErrUnknownUnit    = errors.New("unknown unit type")
	ErrPadOccupied    = errors.New("no free turret slot in that lane")
	ErrNoTurret       = errors.New("no turret in that lane")
	ErrInvalidSlot    = errors.New("invalid turret slot")
	ErrOvertime       = errors.New("locked during overtime")
	ErrMaxLevel       = errors.New("already at maximum level")
	ErrFactoryBuilt   = errors.New("factory already built")
	ErrCouponRequired = errors.New("requires an active coupon")
	ErrOnCooldown     = errors.New("effect is not ready yet")
	ErrUnknownEffect  = errors.New("unknown field effect")
	ErrWrongPhase     = errors.New("action not allowed in this phase")
	ErrUnknownTrack   = errors.New("unknown upgrade track")
	ErrNoStagedUnit   = errors.New("no staged unit to recall")
)

// ---------------------------------------------------------------------------
// Entities
// ---------------------------------------------------------------------------

// Unit is a single fighting entity.
type Unit struct {
	ID            int64    `json:"id"`
	Type          UnitType `json:"type"`
	Side          int      `json:"side"`
	Lane          int      `json:"lane"`
	X             float64  `json:"x"`
	YOff          float64  `json:"yOff"` // cosmetic lane-offset in lane-height fractions (-0.31..0.31)
	HP            float64  `json:"hp"`
	MaxHP         float64  `json:"maxHp"`
	Shield        float64  `json:"shield"`
	MaxShield     float64  `json:"maxShield"`
	Damage        float64  `json:"damage"`
	Cooldown      float64  `json:"-"` // seconds until next attack
	LastHitBy     int      `json:"-"` // side that dealt the last damage, -1 if none
	Staged        bool     `json:"staged"`
	DistanceMoved float64  `json:"-"` // used for Knight Charge
	ChargeReady   bool     `json:"-"`
	BurnUntil     float64  `json:"-"` // incendiary arrow DoT
	BurnTickIn    float64  `json:"-"`
	SpeedBuff     float64  `json:"-"` // fractional attack/move speed bonus for the round
	SpeedBuffEnd  float64  `json:"-"`
	TrackLevels   map[UpgradeTrack]int `json:"-"`
}

// Turret is a lane protective tower standing on a slot of a pad.
type Turret struct {
	Side        int                  `json:"side"`
	Lane        int                  `json:"lane"`
	Slot        int                  `json:"slot"`
	X           float64              `json:"x"`
	Level       int                  `json:"level"`
	HP          float64              `json:"hp"`
	MaxHP       float64              `json:"maxHp"`
	Shield      float64              `json:"shield"`
	MaxShield   float64              `json:"maxShield"`
	Damage      float64              `json:"damage"`
	Cooldown    float64              `json:"-"`
	TargetID    int64                `json:"-"`
	LastHitBy   int                  `json:"-"`
	TrackLevels map[UpgradeTrack]int `json:"trackLevels"`
}

// Cache is a legacy/mid-field supply crate.
type Cache struct {
	ID        int64   `json:"id"`
	Lane      int     `json:"lane"`
	X         float64 `json:"x"`
	Amount    int     `json:"amount"`
	ExpiresAt float64 `json:"expiresAt"`
}

// Anomaly is a random lane feature spawned per round: loot, shrine, neutral monster, or hazard.
type Anomaly struct {
	ID       int64   `json:"id"`
	Kind     string  `json:"kind"` // "loot_coins" | "loot_buff" | "neutral_golem" | "hazard_fire" | "hazard_tar"
	Lane     int     `json:"lane"`
	X        float64 `json:"x"`
	HP       float64 `json:"hp"`
	MaxHP    float64 `json:"maxHp"`
	Damage   float64 `json:"-"`
	Cooldown float64 `json:"-"`
	Value    int     `json:"value"` // coin bounty or amount
	LastHitBy int    `json:"-"`
	TickIn   float64 `json:"-"`
}

// Burn is an active Dragon Fire field effect.
type Burn struct {
	Lane       int
	Side       int     // caster side
	Until      float64 // sim time at which the burn expires
	TickIn     float64 // seconds until the next damage tick
	Interval   float64 // seconds between damage ticks
	TickDamage float64
}

// Player holds per-player economy, soldier pool, and upgrade state.
type Player struct {
	Coins             int                               `json:"coins"`
	Factory           bool                              `json:"factory"`
	FactoryLevel      int                               `json:"factoryLevel"`
	Lives             int                               `json:"lives"`
	UnitUpgrades      map[UnitType]int                  `json:"unitUpgrades"`
	UnitTrackUpgrades map[UnitType]map[UpgradeTrack]int `json:"unitTrackUpgrades"`
	SoldierPool       map[UnitType]int                  `json:"soldierPool"`
	Entitled          bool                              `json:"-"` // updated when actions arrive

	// Match statistics (for the end-of-match report).
	DamageDealt  float64 `json:"damageDealt"`
	Kills        int     `json:"kills"`
	UnitsLost    int     `json:"unitsLost"`
	TurretsDown  int     `json:"turretsDown"`
	BountyEarned int     `json:"bountyEarned"`
}

// EstimatedRoundIncome returns the player's expected round income (base + round scaling + factory + interest).
func (p *Player) EstimatedRoundIncome(cfg Balance, nextRound int) int {
	r := nextRound
	if r < 1 {
		r = 1
	}
	inc := cfg.Economy.BaseIncome + (r-1)*cfg.Economy.RoundStep
	if p.Factory {
		inc += cfg.Economy.FactoryIncome * (p.FactoryLevel + 1)
	}
	interest := int(float64(p.Coins) * cfg.Economy.InterestRate)
	if interest > cfg.Economy.MaxInterest {
		interest = cfg.Economy.MaxInterest
	}
	if interest > 0 {
		inc += interest
	}
	return inc
}

// ---------------------------------------------------------------------------
// Events (drained by the server and forwarded to clients)
// ---------------------------------------------------------------------------

type Event struct {
	Tick   int64  `json:"tick"`
	Side   int    `json:"side"` // SideA, SideB, or SideNone for global news
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Amount int    `json:"amount,omitempty"`
	Lane   int    `json:"lane,omitempty"`
}

// FxEvent is a purely cosmetic combat effect for clients to render.
type FxEvent struct {
	Kind  string  `json:"kind"` // "shot" | "melee" | "rockslide" | "dragonfire" | "warcry" | "burn" | "bounty"
	Unit  string  `json:"unit"`
	Side  int     `json:"side"`
	Lane  int     `json:"lane"`
	FromX float64 `json:"fromX"`
	ToX   float64 `json:"toX"`
	Slot  int     `json:"slot"` // turret slot index; -1 for non-turret FX
}

// DrainFx returns and clears pending FX events.
func (s *Sim) DrainFx() []FxEvent {
	if len(s.fx) == 0 {
		return nil
	}
	out := s.fx
	s.fx = nil
	return out
}

// ---------------------------------------------------------------------------
// The simulation
// ---------------------------------------------------------------------------

type Sim struct {
	cfg Balance
	rng *rand.Rand

	Tick int64   `json:"tick"`
	Time float64 `json:"time"`

	Phase          TurnPhase `json:"phase"`
	Round          int       `json:"round"`
	PhaseRemaining float64   `json:"phaseRemaining"`
	Ready          [2]bool   `json:"ready"`
	combatElapsed  float64

	units   []*Unit
	turrets [2][NumLanes][TurretSlots]*Turret
	players [2]Player

	Overtime     bool    `json:"overtime"`
	TurretFactor float64 `json:"turretFactor"`

	Finished bool `json:"finished"`
	Draw     bool `json:"draw"`
	Winner   int  `json:"winner"`
	WinLane  int  `json:"winLane"`

	nextUnitID   int64
	events       []Event
	fx           []FxEvent
	firstBlood   bool
	caches       []*Cache
	anomalies    []*Anomaly
	streakCount  [2]int
	lastKillAt   [2]float64
	burns        []Burn
	abilityReady [2]map[string]float64
}

// New creates a fresh turn-based match starting in Round 1 Preparation Phase
// with initial protective towers in every lane.
func New(seed int64, cfg Balance) *Sim {
	s := &Sim{
		cfg:            cfg,
		rng:            rand.New(rand.NewSource(seed)),
		Phase:          PhasePrep,
		Round:          1,
		PhaseRemaining: cfg.Turn.PrepDuration,
		Winner:         NoSide,
		WinLane:        -1,
		nextUnitID:     1,
		TurretFactor:   1.0,
	}
	for i := range s.players {
		s.players[i] = Player{
			Coins:             cfg.StartCoins,
			Lives:             1,
			UnitUpgrades:      map[UnitType]int{},
			UnitTrackUpgrades: map[UnitType]map[UpgradeTrack]int{},
			SoldierPool:       map[UnitType]int{},
		}
		s.lastKillAt[i] = -100
		s.abilityReady[i] = map[string]float64{}
	}

	// Place initial protective towers in every lane for both sides.
	initialSlots := cfg.Turn.InitialTowersPerLane
	if initialSlots > TurretSlots {
		initialSlots = TurretSlots
	}
	for side := 0; side < 2; side++ {
		x := cfg.Turret.PadX
		if side == SideB {
			x = FieldLength - cfg.Turret.PadX
		}
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < initialSlots; slot++ {
				s.turrets[side][lane][slot] = &Turret{
					Side:        side,
					Lane:        lane,
					Slot:        slot,
					X:           x,
					Level:       0,
					HP:          cfg.Turret.MaxHP,
					MaxHP:       cfg.Turret.MaxHP,
					Shield:      cfg.Turret.BaseShield,
					MaxShield:   cfg.Turret.BaseShield,
					Damage:      cfg.Turret.Damage,
					LastHitBy:   -1,
					TrackLevels: map[UpgradeTrack]int{},
				}
			}
		}
	}

	s.spawnRoundAnomalies()
	s.addEvent(SideNone, "roundPrep", "Round 1 Preparation — deploy your forces and click Ready!")
	return s
}

func (s *Sim) SetLives(lives int) {
	for i := range s.players {
		s.players[i].Lives = lives
	}
}

// Balance returns the balance table the sim was built with.
func (s *Sim) Balance() Balance { return s.cfg }

func (s *Sim) addEvent(side int, kind, format string, args ...any) {
	s.events = append(s.events, Event{Tick: s.Tick, Side: side, Kind: kind, Text: fmt.Sprintf(format, args...)})
}

// DrainEvents returns and clears pending events.
func (s *Sim) DrainEvents() []Event {
	if len(s.events) == 0 {
		return nil
	}
	out := s.events
	s.events = nil
	return out
}

// ---------------------------------------------------------------------------
// Actions
// ---------------------------------------------------------------------------

// Resign forfeits the match for the given side.
func (s *Sim) Resign(side int) {
	if s.Finished || (side != SideA && side != SideB) {
		return
	}
	s.Finished = true
	s.Winner = 1 - side
	s.WinLane = -1
	s.addEvent(SideNone, "resign", "%s resigned", sideName(side))
}

// ApplyAction validates and applies a player intention.
func (s *Sim) ApplyAction(a Action) error {
	if s.Finished {
		return ErrMatchOver
	}
	if a.Side != SideA && a.Side != SideB {
		return errors.New("invalid side")
	}
	p := &s.players[a.Side]
	p.Entitled = a.Entitled

	switch a.Kind {
	case ActionReady:
		if s.Phase != PhasePrep {
			return ErrWrongPhase
		}
		s.Ready[a.Side] = !s.Ready[a.Side]
		if s.Ready[a.Side] {
			s.addEvent(a.Side, "ready", "%s is Ready!", sideName(a.Side))
		}
		if s.Ready[SideA] && s.Ready[SideB] {
			s.startCombatPhase()
		}
		return nil

	case ActionDeploy:
		if s.Phase != PhasePrep {
			return fmt.Errorf("%w: deploy units during Preparation Phase", ErrWrongPhase)
		}
		if !validLane(a.Lane) {
			return ErrInvalidLane
		}
		ub, ok := s.cfg.Units[a.Unit]
		if !ok {
			return ErrUnknownUnit
		}
		if ub.CouponOnly && !a.Entitled {
			return ErrCouponRequired
		}
		// Use Soldier Pool first (free); otherwise purchase with coins.
		fromPool := p.SoldierPool[a.Unit] > 0
		if !fromPool {
			if p.Coins < ub.Cost {
				return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, ub.Cost)
			}
			p.Coins -= ub.Cost
		} else {
			p.SoldierPool[a.Unit]--
		}
		s.spawnStagedUnit(a.Side, a.Lane, a.Unit)
		return nil

	case ActionRecallUnit:
		if s.Phase != PhasePrep {
			return fmt.Errorf("%w: recall units during Preparation Phase", ErrWrongPhase)
		}
		if !validLane(a.Lane) {
			return ErrInvalidLane
		}
		for i := len(s.units) - 1; i >= 0; i-- {
			u := s.units[i]
			if u.Side == a.Side && u.Lane == a.Lane && u.Staged && (a.Unit == "" || u.Type == a.Unit) {
				p.SoldierPool[u.Type]++
				s.units = append(s.units[:i], s.units[i+1:]...)
				return nil
			}
		}
		return ErrNoStagedUnit

	case ActionBuildTurret:
		if s.Phase != PhasePrep {
			return fmt.Errorf("%w: build towers during Preparation Phase", ErrWrongPhase)
		}
		if !validLane(a.Lane) {
			return ErrInvalidLane
		}
		if s.Overtime {
			return ErrOvertime
		}
		slot := firstFreeTurretSlot(s.turrets[a.Side][a.Lane])
		if slot < 0 {
			return fmt.Errorf("%w: lane %d", ErrPadOccupied, a.Lane+1)
		}
		cost := s.cfg.Turret.Cost
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		x := s.cfg.Turret.PadX
		if a.Side == SideB {
			x = FieldLength - s.cfg.Turret.PadX
		}
		s.turrets[a.Side][a.Lane][slot] = &Turret{
			Side:        a.Side,
			Lane:        a.Lane,
			Slot:        slot,
			X:           x,
			Level:       0,
			HP:          s.cfg.Turret.MaxHP,
			MaxHP:       s.cfg.Turret.MaxHP,
			Shield:      s.cfg.Turret.BaseShield,
			MaxShield:   s.cfg.Turret.BaseShield,
			Damage:      s.cfg.Turret.Damage,
			LastHitBy:   -1,
			TrackLevels: map[UpgradeTrack]int{},
		}
		return nil

	case ActionBuildFactory:
		if s.Phase != PhasePrep {
			return fmt.Errorf("%w: build factory during Preparation Phase", ErrWrongPhase)
		}
		if p.Factory {
			return ErrFactoryBuilt
		}
		cost := s.cfg.Economy.FactoryCost
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		p.Factory = true
		s.addEvent(a.Side, "factory", "%s built a Coin Factory", sideName(a.Side))
		return nil

	case ActionUpgradeFactory:
		if s.Phase != PhasePrep {
			return fmt.Errorf("%w: upgrade factory during Preparation Phase", ErrWrongPhase)
		}
		if !p.Factory {
			return ErrFactoryBuilt
		}
		if p.FactoryLevel >= 2 {
			return ErrMaxLevel
		}
		cost := [2]int{75, 125}[p.FactoryLevel]
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		p.FactoryLevel++
		s.addEvent(a.Side, "factory", "%s upgraded their Coin Factory (Lv%d)", sideName(a.Side), p.FactoryLevel+1)
		return nil

	case ActionUpgradeUnit:
		if s.Phase != PhasePrep {
			return fmt.Errorf("%w: upgrade units during Preparation Phase", ErrWrongPhase)
		}
		ub, ok := s.cfg.Units[a.Unit]
		if !ok {
			return ErrUnknownUnit
		}
		if ub.CouponOnly && !a.Entitled {
			return ErrCouponRequired
		}
		track := a.Track
		if track == "" {
			// Legacy / default track upgrades both HP & Damage via TrackDamage/TrackHP fallback
			level := p.UnitUpgrades[a.Unit]
			if level >= 2 {
				return ErrMaxLevel
			}
			cost := ub.UpgradeCosts[level]
			if p.Coins < cost {
				return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
			}
			p.Coins -= cost
			p.UnitUpgrades[a.Unit] = level + 1
			s.setTrackLevel(p, a.Unit, TrackHP, level+1)
			s.setTrackLevel(p, a.Unit, TrackDamage, level+1)
			s.refreshStagedUnits(a.Side, a.Unit)
			return nil
		}
		if !validTrack(track) {
			return ErrUnknownTrack
		}
		if track == TrackSpecial && !a.Entitled {
			return ErrCouponRequired
		}
		maxLvl := 2
		if track == TrackSpecial {
			maxLvl = 1
		}
		cur := s.getTrackLevel(p, a.Unit, track)
		if cur >= maxLvl {
			return ErrMaxLevel
		}
		costs := ub.TrackCosts[track]
		cost := costs[cur]
		if cost <= 0 {
			cost = ub.UpgradeCosts[cur]
		}
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		s.setTrackLevel(p, a.Unit, track, cur+1)
		if track == TrackHP || track == TrackDamage {
			// Keep aggregate legacy counter in sync for any display
			hpL := s.getTrackLevel(p, a.Unit, TrackHP)
			dmgL := s.getTrackLevel(p, a.Unit, TrackDamage)
			if hpL > dmgL {
				p.UnitUpgrades[a.Unit] = hpL
			} else {
				p.UnitUpgrades[a.Unit] = dmgL
			}
		}
		s.refreshStagedUnits(a.Side, a.Unit)
		return nil

	case ActionUpgradeTurret:
		if s.Phase != PhasePrep {
			return fmt.Errorf("%w: upgrade towers during Preparation Phase", ErrWrongPhase)
		}
		if !validLane(a.Lane) {
			return ErrInvalidLane
		}
		if s.Overtime {
			return ErrOvertime
		}
		slots := s.turrets[a.Side][a.Lane]
		idx := a.Slot
		if idx < 0 {
			idx = firstBuiltTurretSlot(slots)
			if idx < 0 {
				return ErrNoTurret
			}
		} else if idx >= TurretSlots {
			return ErrInvalidSlot
		}
		t := slots[idx]
		if t == nil {
			return ErrNoTurret
		}
		if t.TrackLevels == nil {
			t.TrackLevels = map[UpgradeTrack]int{}
		}
		track := a.Track
		if track == "" {
			if t.Level >= 2 {
				return ErrMaxLevel
			}
			cost := s.cfg.Turret.UpgradeCosts[t.Level]
			if p.Coins < cost {
				return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
			}
			p.Coins -= cost
			t.Level++
			t.MaxHP += s.cfg.Turret.MaxHP * s.cfg.Turret.UpgradeHPPerLevel
			t.Damage = s.cfg.Turret.Damage * (1 + float64(t.Level)*s.cfg.Turret.UpgradeDamagePerLevel)
			return nil
		}
		if !validTrack(track) {
			return ErrUnknownTrack
		}
		if track == TrackSpecial && !a.Entitled {
			return ErrCouponRequired
		}
		maxLvl := 2
		if track == TrackSpecial {
			maxLvl = 1
		}
		cur := t.TrackLevels[track]
		if cur >= maxLvl {
			return ErrMaxLevel
		}
		cost := s.cfg.Turret.TrackCosts[track][cur]
		if cost <= 0 {
			cost = s.cfg.Turret.UpgradeCosts[cur]
		}
		if p.Coins < cost {
			return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cost)
		}
		p.Coins -= cost
		t.TrackLevels[track] = cur + 1
		switch track {
		case TrackHP:
			t.MaxHP += s.cfg.Turret.MaxHP * s.cfg.Turret.UpgradeHPPerLevel
		case TrackDamage:
			t.Damage = s.cfg.Turret.Damage * (1 + float64(t.TrackLevels[TrackDamage])*s.cfg.Turret.UpgradeDamagePerLevel)
		case TrackShield:
			addShield := s.cfg.Turret.MaxHP * 0.25
			t.MaxShield += addShield
			t.Shield += addShield
		}
		t.Level = maxInt(t.TrackLevels[TrackHP], t.TrackLevels[TrackDamage])
		return nil

	case ActionFieldEffect:
		return s.applyFieldEffect(a, p)
	}
	return errors.New("unknown action")
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func validTrack(t UpgradeTrack) bool {
	switch t {
	case TrackDamage, TrackAttackSpeed, TrackHP, TrackShield, TrackSpecial:
		return true
	}
	return false
}

func (s *Sim) getTrackLevel(p *Player, u UnitType, t UpgradeTrack) int {
	if p.UnitTrackUpgrades == nil {
		return 0
	}
	if m := p.UnitTrackUpgrades[u]; m != nil {
		return m[t]
	}
	return 0
}

func (s *Sim) setTrackLevel(p *Player, u UnitType, t UpgradeTrack, lvl int) {
	if p.UnitTrackUpgrades == nil {
		p.UnitTrackUpgrades = map[UnitType]map[UpgradeTrack]int{}
	}
	if p.UnitTrackUpgrades[u] == nil {
		p.UnitTrackUpgrades[u] = map[UpgradeTrack]int{}
	}
	p.UnitTrackUpgrades[u][t] = lvl
}

func (s *Sim) spawnStagedUnit(side, lane int, unitType UnitType) {
	ub := s.cfg.Units[unitType]
	p := &s.players[side]
	hpLvl := s.getTrackLevel(p, unitType, TrackHP)
	dmgLvl := s.getTrackLevel(p, unitType, TrackDamage)
	shdLvl := s.getTrackLevel(p, unitType, TrackShield)
	spdLvl := s.getTrackLevel(p, unitType, TrackAttackSpeed)
	spcLvl := s.getTrackLevel(p, unitType, TrackSpecial)

	hpMul := 1 + float64(hpLvl)*ub.UpgradeHP
	dmgMul := 1 + float64(dmgLvl)*ub.UpgradeDamage
	shield := ub.BaseShield + float64(shdLvl)*ub.MaxHP*ub.UpgradeShield

	if spcLvl > 0 {
		switch unitType {
		case Soldier:
			shield += 15
		case Ram:
			shield += 50
		case Mage:
			dmgMul += 0.20
		case Siege:
			dmgMul += 0.35
		case Valkyrie:
			hpMul += 0.20
			dmgMul += 0.20
			shield += 30
		case Dragon:
			hpMul += 0.20
			dmgMul += 0.20
			shield += 40
		}
	}

	// Count staged units in this lane for clean formation offset
	countInLane := 0
	for _, u := range s.units {
		if u.Side == side && u.Lane == lane && u.Staged {
			countInLane++
		}
	}
	x := s.cfg.SpawnInset + float64(countInLane%4)*1.4
	if side == SideB {
		x = FieldLength - x
	}

	tracks := map[UpgradeTrack]int{
		TrackHP:          hpLvl,
		TrackDamage:      dmgLvl,
		TrackShield:      shdLvl,
		TrackAttackSpeed: spdLvl,
		TrackSpecial:     spcLvl,
	}

	s.units = append(s.units, &Unit{
		ID:          s.nextUnitID,
		Type:        unitType,
		Side:        side,
		Lane:        lane,
		X:           x,
		YOff:        (s.rng.Float64() - 0.5) * 0.60,
		HP:          ub.MaxHP * hpMul,
		MaxHP:       ub.MaxHP * hpMul,
		Shield:      shield,
		MaxShield:   shield,
		Damage:      ub.Damage * dmgMul,
		Cooldown:    0,
		LastHitBy:   -1,
		Staged:      true,
		TrackLevels: tracks,
	})
	s.nextUnitID++
}

// refreshStagedUnits updates stats of already-staged units in Prep Phase when an upgrade is bought.
func (s *Sim) refreshStagedUnits(side int, unitType UnitType) {
	ub := s.cfg.Units[unitType]
	p := &s.players[side]
	hpLvl := s.getTrackLevel(p, unitType, TrackHP)
	dmgLvl := s.getTrackLevel(p, unitType, TrackDamage)
	shdLvl := s.getTrackLevel(p, unitType, TrackShield)
	spdLvl := s.getTrackLevel(p, unitType, TrackAttackSpeed)
	spcLvl := s.getTrackLevel(p, unitType, TrackSpecial)

	hpMul := 1 + float64(hpLvl)*ub.UpgradeHP
	dmgMul := 1 + float64(dmgLvl)*ub.UpgradeDamage
	shield := ub.BaseShield + float64(shdLvl)*ub.MaxHP*ub.UpgradeShield
	if spcLvl > 0 {
		switch unitType {
		case Soldier:
			shield += 15
		case Ram:
			shield += 50
		case Mage:
			dmgMul += 0.20
		case Siege:
			dmgMul += 0.35
		case Valkyrie:
			hpMul += 0.20
			dmgMul += 0.20
			shield += 30
		case Dragon:
			hpMul += 0.20
			dmgMul += 0.20
			shield += 40
		}
	}

	for _, u := range s.units {
		if u.Side == side && u.Type == unitType && u.Staged {
			u.MaxHP = ub.MaxHP * hpMul
			u.HP = u.MaxHP
			u.MaxShield = shield
			u.Shield = shield
			u.Damage = ub.Damage * dmgMul
			u.TrackLevels = map[UpgradeTrack]int{
				TrackHP: hpLvl, TrackDamage: dmgLvl, TrackShield: shdLvl, TrackAttackSpeed: spdLvl, TrackSpecial: spcLvl,
			}
		}
	}
}

// effectConfig resolves a field-effect id to its balance row.
func (s *Sim) effectConfig(id string) (FieldEffectBalance, bool) {
	switch id {
	case EffectRockslide:
		return s.cfg.Coupon.Rockslide, true
	case EffectDragonFire:
		return s.cfg.Coupon.DragonFire, true
	case EffectWarcry:
		return s.cfg.Coupon.Warcry, true
	default:
		return FieldEffectBalance{}, false
	}
}

func (s *Sim) applyFieldEffect(a Action, p *Player) error {
	if !validLane(a.Lane) {
		return ErrInvalidLane
	}
	cfg, ok := s.effectConfig(a.Effect)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownEffect, a.Effect)
	}
	if !a.Entitled {
		return ErrCouponRequired
	}
	if readyAt := s.abilityReady[a.Side][a.Effect]; s.Time < readyAt {
		return fmt.Errorf("%w: %.0fs", ErrOnCooldown, readyAt-s.Time)
	}
	if p.Coins < cfg.Cost {
		return fmt.Errorf("%w: need %d", ErrNotEnoughCoins, cfg.Cost)
	}
	p.Coins -= cfg.Cost
	s.abilityReady[a.Side][a.Effect] = s.Time + cfg.Cooldown

	switch a.Effect {
	case EffectRockslide:
		damaged := 0
		for _, u := range s.units {
			if u.Side == a.Side || u.Lane != a.Lane || u.HP <= 0 {
				continue
			}
			if s.cfg.Units[u.Type].Flying {
				continue
			}
			s.dealDamage(a.Side, u, cfg.Damage, 1.0)
			damaged++
		}
		s.addEvent(a.Side, "rockslide", "%s called a Rockslide down on lane %d! (%d enemies hit)", sideName(a.Side), a.Lane+1, damaged)
		s.fx = append(s.fx, FxEvent{Kind: "rockslide", Side: a.Side, Lane: a.Lane, Slot: -1})

	case EffectDragonFire:
		duration := cfg.Duration
		if duration <= 0 {
			duration = 6
		}
		interval := cfg.TickInterval
		if interval <= 0 {
			interval = 0.5
		}
		s.burns = append(s.burns, Burn{
			Lane:       a.Lane,
			Side:       a.Side,
			Until:      s.Time + duration,
			TickIn:     interval,
			Interval:   interval,
			TickDamage: cfg.Damage,
		})
		s.addEvent(a.Side, "dragonfire", "%s set lane %d ablaze with Dragon Fire!", sideName(a.Side), a.Lane+1)
		s.fx = append(s.fx, FxEvent{Kind: "dragonfire", Side: a.Side, Lane: a.Lane, Slot: -1})

	case EffectWarcry:
		buffed := 0
		dur := cfg.Duration
		if dur <= 0 {
			dur = 8
		}
		for _, u := range s.units {
			if u.Side == a.Side && u.Lane == a.Lane && u.HP > 0 {
				u.Shield += cfg.Damage
				u.MaxShield = math.Max(u.MaxShield, u.Shield)
				u.SpeedBuff = math.Max(u.SpeedBuff, 0.35)
				u.SpeedBuffEnd = s.Time + dur
				buffed++
			}
		}
		s.addEvent(a.Side, "warcry", "%s sounded a Warcry in lane %d! (%d allies empowered)", sideName(a.Side), a.Lane+1, buffed)
		s.fx = append(s.fx, FxEvent{Kind: "warcry", Side: a.Side, Lane: a.Lane, Slot: -1})
	}
	return nil
}

func validLane(l int) bool { return l >= 0 && l < NumLanes }

func firstFreeTurretSlot(slots [TurretSlots]*Turret) int {
	for i, t := range slots {
		if t == nil {
			return i
		}
	}
	return -1
}

func firstBuiltTurretSlot(slots [TurretSlots]*Turret) int {
	for i, t := range slots {
		if t != nil {
			return i
		}
	}
	return -1
}

const timerEpsilon = 1e-9

func sideName(side int) string {
	if side == SideA {
		return "Blue"
	}
	return "Red"
}

// ---------------------------------------------------------------------------
// Turn Transitions & Anomalies
// ---------------------------------------------------------------------------

func (s *Sim) startCombatPhase() {
	s.Phase = PhaseCombat
	s.PhaseRemaining = s.cfg.Turn.CombatDuration
	s.Ready = [2]bool{}
	s.combatElapsed = 0
	for _, u := range s.units {
		u.Staged = false
		u.DistanceMoved = 0
		u.ChargeReady = false
	}
	s.addEvent(SideNone, "combatStart", "Round %d Combat Begins!", s.Round)
}

func (s *Sim) endCombatPhase() {
	// Return surviving units to each player's Soldier Pool!
	returned := [2]int{}
	for _, u := range s.units {
		if u.HP > 0 && (u.Side == SideA || u.Side == SideB) {
			s.players[u.Side].SoldierPool[u.Type]++
			returned[u.Side]++
		}
	}
	s.units = nil
	s.burns = nil

	// Recharge turret shields between rounds
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < TurretSlots; slot++ {
				if t := s.turrets[side][lane][slot]; t != nil {
					t.Shield = t.MaxShield
				}
			}
		}
	}

	if s.Round >= s.cfg.Turn.MaxRounds {
		s.resolveMaxRoundsWinner()
		return
	}

	s.Round++
	if s.Round >= s.cfg.Turn.EscalationStartRound && !s.Overtime {
		s.Overtime = true
		s.addEvent(SideNone, "overtime", "ESCALATION — Units march faster and deal bonus damage to towers!")
	}

	// Grant round income + interest + factory + optional windfall + coupon stipend
	windfall := 0
	if s.cfg.Economy.WindfallMax >= s.cfg.Economy.WindfallMin && s.cfg.Economy.WindfallMax > 0 {
		windfall = s.cfg.Economy.WindfallMin + s.rng.Intn(s.cfg.Economy.WindfallMax-s.cfg.Economy.WindfallMin+1)
	}
	for side := 0; side < 2; side++ {
		p := &s.players[side]
		inc := p.EstimatedRoundIncome(s.cfg, s.Round) + windfall
		if p.Entitled {
			inc += s.cfg.Coupon.RoundBonus
		}
		p.Coins += inc
	}

	s.Phase = PhasePrep
	s.PhaseRemaining = s.cfg.Turn.PrepDuration
	s.Ready = [2]bool{}
	s.spawnRoundAnomalies()
	s.addEvent(SideNone, "roundPrep", "Round %d Prep — Survivors returned (Blue %d, Red %d). Shared windfall +%dc!", s.Round, returned[0], returned[1], windfall)
}

func (s *Sim) resolveMaxRoundsWinner() {
	s.Finished = true
	a, b := &s.players[SideA], &s.players[SideB]
	if a.Lives != b.Lives {
		if a.Lives > b.Lives {
			s.Winner = SideA
		} else {
			s.Winner = SideB
		}
		s.addEvent(SideNone, "breach", "%s wins after %d rounds (Lives %d vs %d)!", sideName(s.Winner), s.Round, a.Lives, b.Lives)
		return
	}
	// Tie-break by surviving towers, then kills
	towersA, towersB := s.countTurrets(SideA), s.countTurrets(SideB)
	if towersA != towersB {
		if towersA > towersB {
			s.Winner = SideA
		} else {
			s.Winner = SideB
		}
		s.addEvent(SideNone, "breach", "%s wins on tiebreaker (Towers %d vs %d)!", sideName(s.Winner), towersA, towersB)
		return
	}
	if a.Kills != b.Kills {
		if a.Kills > b.Kills {
			s.Winner = SideA
		} else {
			s.Winner = SideB
		}
		s.addEvent(SideNone, "breach", "%s wins on tiebreaker (Kills %d vs %d)!", sideName(s.Winner), a.Kills, b.Kills)
		return
	}
	s.Draw = true
	s.Winner = NoSide
	s.addEvent(SideNone, "draw", "Final round ended in an exact tie — Draw!")
}

func (s *Sim) countTurrets(side int) int {
	n := 0
	for lane := 0; lane < NumLanes; lane++ {
		for slot := 0; slot < TurretSlots; slot++ {
			if s.turrets[side][lane][slot] != nil {
				n++
			}
		}
	}
	return n
}

func (s *Sim) spawnRoundAnomalies() {
	s.anomalies = nil
	ab := s.cfg.Anomaly
	minCount, maxCount := ab.MinPerRound, ab.MaxPerRound
	if maxCount <= 0 {
		return
	}
	if minCount > maxCount {
		minCount = maxCount
	}
	count := minCount
	if maxCount > minCount {
		count += s.rng.Intn(maxCount - minCount + 1)
	}
	kinds := []string{"loot_coins", "loot_buff", "neutral_golem", "hazard_fire", "hazard_tar"}
	usedLanes := map[int]bool{}
	for i := 0; i < count; i++ {
		lane := s.rng.Intn(NumLanes)
		for tries := 0; tries < 5 && usedLanes[lane]; tries++ {
			lane = s.rng.Intn(NumLanes)
		}
		usedLanes[lane] = true
		kind := kinds[s.rng.Intn(len(kinds))]
		x := 38.0 + s.rng.Float64()*24.0
		anom := &Anomaly{
			ID:        s.nextUnitID,
			Kind:      kind,
			Lane:      lane,
			X:         x,
			LastHitBy: -1,
			TickIn:    0.5,
		}
		switch kind {
		case "loot_coins":
			anom.Value = ab.CoinAmount
		case "loot_buff":
			anom.Value = int(ab.BuffShield)
		case "neutral_golem":
			anom.HP = ab.MonsterHP
			anom.MaxHP = ab.MonsterHP
			anom.Damage = ab.MonsterDamage
			anom.Value = ab.MonsterBounty
		case "hazard_fire", "hazard_tar":
			// passive environmental hazard
		}
		s.nextUnitID++
		s.anomalies = append(s.anomalies, anom)
	}
}

// ---------------------------------------------------------------------------
// Step
// ---------------------------------------------------------------------------

// Step advances the simulation by dt seconds. Callers should use TickDT.
func (s *Sim) Step(dt float64) {
	if s.Finished {
		return
	}
	s.Tick++
	s.Time += dt

	s.PhaseRemaining -= dt
	if s.Phase == PhasePrep {
		if s.PhaseRemaining <= timerEpsilon {
			s.startCombatPhase()
		}
		return
	}

	// --- Combat Phase ---
	s.combatElapsed += dt

	// Kill streak decay
	for side := 0; side < 2; side++ {
		if s.streakCount[side] > 0 && s.Time-s.lastKillAt[side] > 3.0 {
			s.streakCount[side] = 0
		}
	}

	// Dragon Fire burn zones & Incendiary Arrow DoTs
	s.stepBurns(dt)

	// Anomalies: loot pickups, neutral monster combat, environmental hazards
	s.stepAnomalies(dt)

	// Combat & movement
	s.stepUnits(dt)
	s.stepTurrets(dt)
	s.removeDead()

	// Win / breach detection
	s.checkEnd()
	if s.Finished {
		return
	}

	// End combat round when timer expires or all field units are gone after 4s
	if s.PhaseRemaining <= timerEpsilon || (s.combatElapsed >= 4.0 && len(s.units) == 0) {
		s.endCombatPhase()
	}
}

func (s *Sim) stepBurns(dt float64) {
	for i := 0; i < len(s.burns); {
		b := &s.burns[i]
		if s.Time >= b.Until {
			s.burns = append(s.burns[:i], s.burns[i+1:]...)
			continue
		}
		b.TickIn -= dt
		for b.TickIn <= timerEpsilon {
			for _, u := range s.units {
				if u.Side != b.Side && u.Lane == b.Lane && u.HP > 0 {
					s.dealDamage(b.Side, u, b.TickDamage, 1.0)
				}
			}
			s.fx = append(s.fx, FxEvent{Kind: "burn", Side: b.Side, Lane: b.Lane, Slot: -1})
			b.TickIn += b.Interval
		}
		i++
	}
	// Unit-level incendiary burns
	for _, u := range s.units {
		if u.HP > 0 && u.BurnUntil > s.Time {
			u.BurnTickIn -= dt
			if u.BurnTickIn <= 0 {
				u.BurnTickIn += 0.5
				s.dealDamage(1-u.Side, u, 2.5, 1.0)
			}
		}
	}
}

func (s *Sim) stepAnomalies(dt float64) {
	ab := s.cfg.Anomaly
	for i := 0; i < len(s.anomalies); {
		an := s.anomalies[i]
		switch an.Kind {
		case "loot_coins":
			claimed := -1
			for _, u := range s.units {
				if u.Lane == an.Lane && u.HP > 0 && absDist(u.X, an.X) <= ab.PickupRadius {
					claimed = u.Side
					break
				}
			}
			if claimed >= 0 {
				s.players[claimed].Coins += an.Value
				s.players[claimed].BountyEarned += an.Value
				s.addEvent(claimed, "supplyClaim", "%s claimed +%dc loot in lane %d!", sideName(claimed), an.Value, an.Lane+1)
				s.events[len(s.events)-1].Lane = an.Lane
				s.events[len(s.events)-1].Amount = an.Value
				s.anomalies = append(s.anomalies[:i], s.anomalies[i+1:]...)
				continue
			}

		case "loot_buff":
			claimed := -1
			for _, u := range s.units {
				if u.Lane == an.Lane && u.HP > 0 && absDist(u.X, an.X) <= ab.PickupRadius {
					claimed = u.Side
					break
				}
			}
			if claimed >= 0 {
				for _, u := range s.units {
					if u.Side == claimed && u.Lane == an.Lane && u.HP > 0 {
						u.Shield += float64(an.Value)
						u.MaxShield = math.Max(u.MaxShield, u.Shield)
						u.SpeedBuff = math.Max(u.SpeedBuff, ab.BuffSpeedBonus)
						u.SpeedBuffEnd = s.Time + 15.0
					}
				}
				s.addEvent(claimed, "warcry", "%s claimed the Shrine in lane %d! (+Speed & +%d Shield)", sideName(claimed), an.Lane+1, an.Value)
				s.anomalies = append(s.anomalies[:i], s.anomalies[i+1:]...)
				continue
			}

		case "neutral_golem":
			if an.HP <= 0 {
				if killer := an.LastHitBy; killer == SideA || killer == SideB {
					s.players[killer].Coins += an.Value
					s.players[killer].BountyEarned += an.Value
					s.addEvent(killer, "bounty", "%s slew the Lane %d Guardian! (+%dc bounty)", sideName(killer), an.Lane+1, an.Value)
					s.events[len(s.events)-1].Lane = an.Lane
					s.events[len(s.events)-1].Amount = an.Value
				}
				s.anomalies = append(s.anomalies[:i], s.anomalies[i+1:]...)
				continue
			}
			an.Cooldown -= dt
			if an.Cooldown <= 0 {
				var target *Unit
				bestD := math.Inf(1)
				for _, u := range s.units {
					if u.Lane == an.Lane && u.HP > 0 {
						if d := absDist(u.X, an.X); d <= ab.MonsterRange && d < bestD {
							bestD = d
							target = u
						}
					}
				}
				if target != nil {
					s.dealDamage(SideNone, target, an.Damage, 1.0)
					an.Cooldown = 1.2
					s.fx = append(s.fx, FxEvent{Kind: "melee", Unit: "golem", Side: SideNone, Lane: an.Lane, FromX: an.X, ToX: target.X, Slot: -1})
				}
			}

		case "hazard_fire":
			an.TickIn -= dt
			if an.TickIn <= 0 {
				an.TickIn += 0.5
				for _, u := range s.units {
					if u.Lane == an.Lane && u.HP > 0 && !s.cfg.Units[u.Type].Flying && absDist(u.X, an.X) <= ab.PickupRadius*1.4 {
						s.dealDamage(SideNone, u, ab.HazardDPS*0.5, 1.0)
					}
				}
			}
		}
		i++
	}
}

func (s *Sim) dealDamage(attackerSide int, target any, dmg float64, shieldMult float64) {
	if dmg <= 0 {
		return
	}
	if shieldMult <= 0 {
		shieldMult = 1.0
	}
	switch t := target.(type) {
	case *Unit:
		// Flat armor reduction from Shield track
		armor := float64(t.TrackLevels[TrackShield]) * 1.5
		effDmg := math.Max(1.0, dmg-armor)
		dealt := 0.0
		if t.Shield > 0 {
			shieldDmg := effDmg * shieldMult
			if shieldDmg >= t.Shield {
				usedRatio := t.Shield / shieldDmg
				dealt += t.Shield
				t.Shield = 0
				remDmg := effDmg * (1.0 - usedRatio)
				hpDmg := math.Min(t.HP, remDmg)
				t.HP -= hpDmg
				dealt += hpDmg
			} else {
				t.Shield -= shieldDmg
				dealt += shieldDmg
			}
		} else {
			hpDmg := math.Min(t.HP, effDmg)
			t.HP -= hpDmg
			dealt += hpDmg
		}
		if attackerSide == SideA || attackerSide == SideB {
			s.players[attackerSide].DamageDealt += dealt
			t.LastHitBy = attackerSide
		}
	case *Turret:
		armor := float64(t.TrackLevels[TrackShield]) * 1.5
		effDmg := math.Max(1.0, dmg-armor)
		dealt := 0.0
		if t.Shield > 0 {
			shieldDmg := effDmg * shieldMult
			if shieldDmg >= t.Shield {
				usedRatio := t.Shield / shieldDmg
				dealt += t.Shield
				t.Shield = 0
				remDmg := effDmg * (1.0 - usedRatio)
				hpDmg := math.Min(t.HP, remDmg)
				t.HP -= hpDmg
				dealt += hpDmg
			} else {
				t.Shield -= shieldDmg
				dealt += shieldDmg
			}
		} else {
			hpDmg := math.Min(t.HP, effDmg)
			t.HP -= hpDmg
			dealt += hpDmg
		}
		if attackerSide == SideA || attackerSide == SideB {
			s.players[attackerSide].DamageDealt += dealt
			t.LastHitBy = attackerSide
		}
	case *Anomaly:
		hpDmg := math.Min(t.HP, dmg)
		t.HP -= hpDmg
		if attackerSide == SideA || attackerSide == SideB {
			s.players[attackerSide].DamageDealt += hpDmg
			t.LastHitBy = attackerSide
		}
	}
}

func direction(side int) float64 {
	if side == SideA {
		return 1
	}
	return -1
}

func absDist(a, b float64) float64 { return math.Abs(a - b) }

func (s *Sim) counterMultiplier(attacker UnitType, targetClass UnitClass, attackerTracks map[UpgradeTrack]int) float64 {
	ub := s.cfg.Units[attacker]
	mult := 1.0
	if ub.CounterMultipliers != nil {
		if m, ok := ub.CounterMultipliers[targetClass]; ok {
			mult = m
		}
	}
	if attackerTracks[TrackSpecial] > 0 {
		switch {
		case attacker == Soldier && (targetClass == ClassCavalry || targetClass == ClassHeavy):
			mult += 0.25
		case attacker == Bowman && targetClass == ClassStructure:
			mult += 0.25
		case attacker == Ram && targetClass == ClassStructure:
			mult *= 1.40
		}
	}
	if targetClass == ClassStructure && s.Round >= s.cfg.Turn.EscalationStartRound {
		extra := float64(s.Round-s.cfg.Turn.EscalationStartRound+1) * 0.20
		mult *= (1.0 + extra)
	}
	return mult
}

func (s *Sim) stepUnits(dt float64) {
	// Precompute Valkyrie aura per side/lane
	valkAura := [2][NumLanes]bool{}
	for _, u := range s.units {
		if u.HP > 0 && u.Type == Valkyrie {
			valkAura[u.Side][u.Lane] = true
		}
	}

	for _, u := range s.units {
		if u.HP <= 0 || u.Staged {
			continue
		}
		if u.SpeedBuff > 0 && s.Time >= u.SpeedBuffEnd {
			u.SpeedBuff = 0
		}
		ub := s.cfg.Units[u.Type]
		atkRateMult := 1.0 + float64(u.TrackLevels[TrackAttackSpeed])*ub.UpgradeSpeed + u.SpeedBuff
		if valkAura[u.Side][u.Lane] && u.Type != Valkyrie {
			atkRateMult += 0.15
		}
		u.Cooldown -= dt * atkRateMult

		// Siege engines hold position when an enemy is inside minimum range
		if u.Type == Siege {
			if d, ok := s.nearestEnemyDist(u); ok && d < ub.MinRange {
				continue
			}
		}

		target := s.findTarget(u)
		shieldMult := ub.ShieldMultiplier
		if shieldMult <= 0 {
			shieldMult = 1.0
		}

		switch t := target.(type) {
		case *Unit:
			if u.Cooldown <= 0 {
				tClass := s.cfg.Units[t.Type].Class
				dmg := u.Damage * s.counterMultiplier(u.Type, tClass, u.TrackLevels)
				if ub.ChargeBonus > 1.0 && u.ChargeReady {
					bonus := ub.ChargeBonus
					if u.TrackLevels[TrackSpecial] > 0 {
						bonus += 0.20
					}
					dmg *= bonus
					u.ChargeReady = false
				}
				splash := ub.Splash
				if u.Type == Mage && u.TrackLevels[TrackSpecial] > 0 {
					splash *= 1.5
				} else if u.Type == Siege && u.TrackLevels[TrackSpecial] > 0 {
					splash *= 1.35
				}
				if splash > 0 {
					s.splashDamage(u.Side, u.Lane, t.X, splash, dmg, shieldMult, ub.CanHitFlying)
				} else {
					s.dealDamage(u.Side, t, dmg, shieldMult)
					// Reflective Bulwark (Shieldbearer special)
					if t.Type == Shieldbearer && t.TrackLevels[TrackSpecial] > 0 && !ub.Flying {
						s.dealDamage(t.Side, u, dmg*0.25, 1.0)
					}
					// Bowman Incendiary Arrows
					if u.Type == Bowman && u.TrackLevels[TrackSpecial] > 0 {
						t.BurnUntil = s.Time + 3.0
						t.BurnTickIn = 0.5
					}
				}
				if u.Type == Knight && u.TrackLevels[TrackSpecial] > 0 && t.HP <= 0 {
					u.ChargeReady = true
				}
				u.Cooldown = ub.AttackInterval
				s.emitAttackFx(u, t.X)
			}

		case *Anomaly:
			if u.Cooldown <= 0 {
				dmg := u.Damage * s.counterMultiplier(u.Type, ClassHeavy, u.TrackLevels)
				s.dealDamage(u.Side, t, dmg, shieldMult)
				u.Cooldown = ub.AttackInterval
				s.emitAttackFx(u, t.X)
			}

		case *Turret:
			if u.Cooldown <= 0 {
				dmg := u.Damage * s.counterMultiplier(u.Type, ClassStructure, u.TrackLevels)
				if ub.VsTurretDamage > 0 {
					dmgLvl := u.TrackLevels[TrackDamage]
					mul := 1 + float64(dmgLvl)*ub.UpgradeDamage
					if u.Type == Ram && u.TrackLevels[TrackSpecial] > 0 {
						mul *= 1.40
					} else if u.Type == Siege && u.TrackLevels[TrackSpecial] > 0 {
						mul *= 1.35
					}
					if s.Round >= s.cfg.Turn.EscalationStartRound {
						mul *= (1.0 + float64(s.Round-s.cfg.Turn.EscalationStartRound+1)*0.20)
					}
					dmg = ub.VsTurretDamage * mul
				}
				s.dealDamage(u.Side, t, dmg, shieldMult)
				u.Cooldown = ub.AttackInterval
				s.emitAttackFx(u, t.X)
			}

		default:
			moveSpeed := ub.Speed * (1.0 + float64(u.TrackLevels[TrackAttackSpeed])*0.07 + u.SpeedBuff*0.5)
			if s.Round >= s.cfg.Turn.EscalationStartRound {
				moveSpeed *= (1.0 + float64(s.Round-s.cfg.Turn.EscalationStartRound+1)*0.12)
			}
			if !ub.Flying && s.inTarHazard(u.Lane, u.X) {
				moveSpeed *= (1.0 - s.cfg.Anomaly.HazardSlow)
			}
			stepDist := moveSpeed * dt
			nextX := u.X + direction(u.Side)*stepDist

			// Ground units cannot walk past an alive enemy protective tower in their lane
			if !ub.Flying {
				if blockX, blocked := s.enemyTurretBlockX(u.Side, u.Lane); blocked {
					if u.Side == SideA && nextX > blockX-1.5 {
						nextX = math.Max(u.X, blockX-1.5)
					} else if u.Side == SideB && nextX < blockX+1.5 {
						nextX = math.Min(u.X, blockX+1.5)
					}
				}
			}

			moved := math.Abs(nextX - u.X)
			u.X = nextX
			u.DistanceMoved += moved
			if ub.ChargeBonus > 1.0 && u.DistanceMoved >= 12.0 {
				u.ChargeReady = true
			}
		}
	}
}

func (s *Sim) inTarHazard(lane int, x float64) bool {
	for _, an := range s.anomalies {
		if an.Kind == "hazard_tar" && an.Lane == lane && absDist(x, an.X) <= s.cfg.Anomaly.PickupRadius*1.5 {
			return true
		}
	}
	return false
}

func (s *Sim) enemyTurretBlockX(side, lane int) (float64, bool) {
	enemy := 1 - side
	for slot := 0; slot < TurretSlots; slot++ {
		if t := s.turrets[enemy][lane][slot]; t != nil && t.HP > 0 {
			return t.X, true
		}
	}
	return 0, false
}

func (s *Sim) findTarget(u *Unit) any {
	ub := s.cfg.Units[u.Type]

	// Rams prioritize enemy turrets when in range
	if u.Type == Ram {
		if tr := s.nearestEnemyTurret(u, ub.Range); tr != nil {
			return tr
		}
		if eu := s.nearestEnemyUnit(u, ub.Range); eu != nil {
			return eu
		}
		return nil
	}

	if u.Type == Siege {
		if tr := s.nearestEnemyTurret(u, ub.Range); tr != nil && absDist(u.X, tr.X) >= ub.MinRange {
			return tr
		}
		if eu := s.nearestEnemyUnit(u, ub.Range); eu != nil && absDist(u.X, eu.X) >= ub.MinRange {
			return eu
		}
		return nil
	}

	// Standard troops: fight enemy units -> neutral golems -> enemy towers
	if eu := s.nearestEnemyUnit(u, ub.Range); eu != nil {
		return eu
	}
	if an := s.nearestNeutralMonster(u, ub.Range); an != nil {
		return an
	}
	if tr := s.nearestEnemyTurret(u, ub.Range); tr != nil {
		return tr
	}
	return nil
}

func (s *Sim) splashDamage(side, lane int, centerX, radius, dmg, shieldMult float64, hitFlying bool) {
	for _, u := range s.units {
		if u.Side == side || u.Lane != lane || u.HP <= 0 {
			continue
		}
		if s.cfg.Units[u.Type].Flying && !hitFlying {
			continue
		}
		if absDist(u.X, centerX) <= radius {
			s.dealDamage(side, u, dmg, shieldMult)
		}
	}
}

func (s *Sim) nearestEnemyUnit(u *Unit, maxRange float64) *Unit {
	ub := s.cfg.Units[u.Type]
	var best *Unit
	bestD := math.Inf(1)
	for _, e := range s.units {
		if e.Side == u.Side || e.Lane != u.Lane || e.HP <= 0 {
			continue
		}
		if s.cfg.Units[e.Type].Flying && !ub.CanHitFlying && !ub.Flying {
			continue
		}
		d := absDist(u.X, e.X)
		if d <= maxRange && d < bestD {
			best, bestD = e, d
		}
	}
	return best
}

func (s *Sim) nearestNeutralMonster(u *Unit, maxRange float64) *Anomaly {
	var best *Anomaly
	bestD := math.Inf(1)
	for _, an := range s.anomalies {
		if an.Kind != "neutral_golem" || an.Lane != u.Lane || an.HP <= 0 {
			continue
		}
		d := absDist(u.X, an.X)
		if d <= maxRange && d < bestD {
			best, bestD = an, d
		}
	}
	return best
}

func (s *Sim) nearestEnemyTurret(u *Unit, maxRange float64) *Turret {
	enemy := 1 - u.Side
	var best *Turret
	bestD := math.Inf(1)
	if u.Lane >= 0 && u.Lane < NumLanes {
		for slot := 0; slot < TurretSlots; slot++ {
			t := s.turrets[enemy][u.Lane][slot]
			if t == nil || t.HP <= 0 {
				continue
			}
			d := absDist(u.X, t.X)
			if d <= maxRange && d < bestD {
				best, bestD = t, d
			}
		}
	}
	return best
}

func (s *Sim) nearestEnemyDist(u *Unit) (float64, bool) {
	d := math.Inf(1)
	found := false
	for _, e := range s.units {
		if e.Side == u.Side || e.Lane != u.Lane || e.HP <= 0 || s.cfg.Units[e.Type].Flying {
			continue
		}
		if dd := absDist(u.X, e.X); dd < d {
			d, found = dd, true
		}
	}
	for slot := 0; slot < TurretSlots; slot++ {
		if t := s.turrets[1-u.Side][u.Lane][slot]; t != nil && t.HP > 0 {
			if dd := absDist(u.X, t.X); dd < d {
				d, found = dd, true
			}
		}
	}
	return d, found
}

func (s *Sim) emitAttackFx(u *Unit, targetX float64) {
	kind := "melee"
	if u.Type == Bowman || u.Type == Mage || u.Type == Siege || u.Type == Dragon {
		kind = "shot"
	}
	s.fx = append(s.fx, FxEvent{Kind: kind, Unit: string(u.Type), Side: u.Side, Lane: u.Lane, FromX: u.X, ToX: targetX, Slot: -1})
}

func (s *Sim) stepTurrets(dt float64) {
	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < TurretSlots; slot++ {
				t := s.turrets[side][lane][slot]
				if t == nil || t.HP <= 0 {
					continue
				}
				atkRate := 1.0 + float64(t.TrackLevels[TrackAttackSpeed])*0.20
				if t.TrackLevels[TrackSpecial] > 0 {
					atkRate += 0.25
				}
				t.Cooldown -= dt * atkRate

				canHitFlying := t.TrackLevels[TrackSpecial] > 0
				target := s.unitByID(t.TargetID)
				if target != nil && (target.Side == side || target.Lane != lane || absDist(target.X, t.X) > s.cfg.Turret.Range || target.HP <= 0 || (s.cfg.Units[target.Type].Flying && !canHitFlying)) {
					target = nil
					t.TargetID = 0
				}

				if target == nil {
					target = s.pickTurretTarget(t, side, lane, canHitFlying)
					if target != nil {
						t.TargetID = target.ID
					}
				}

				if target != nil && t.Cooldown <= 0 {
					dmg := t.Damage * s.TurretFactor
					if target.Type == Shieldbearer {
						dmg *= 0.65
					}
					s.dealDamage(side, target, dmg, 1.0)
					t.Cooldown = s.cfg.Turret.AttackInterval
					s.fx = append(s.fx, FxEvent{Kind: "shot", Unit: "turret", Side: side, Lane: lane, FromX: t.X, ToX: target.X, Slot: slot})
				}
			}
		}
	}
}

func (s *Sim) pickTurretTarget(t *Turret, side, lane int, canHitFlying bool) *Unit {
	var best *Unit
	for _, u := range s.units {
		if u.Side == side || u.Lane != lane || u.HP <= 0 {
			continue
		}
		if s.cfg.Units[u.Type].Flying && !canHitFlying {
			continue
		}
		if absDist(u.X, t.X) > s.cfg.Turret.Range {
			continue
		}
		if best == nil {
			best = u
			continue
		}
		better := false
		if side == SideA {
			better = u.X < best.X
		} else {
			better = u.X > best.X
		}
		if better {
			best = u
		}
	}
	return best
}

func (s *Sim) unitByID(id int64) *Unit {
	if id == 0 {
		return nil
	}
	for _, u := range s.units {
		if u.ID == id {
			return u
		}
	}
	return nil
}

func (s *Sim) unitBounty(u *Unit) int {
	ub := s.cfg.Units[u.Type]
	trackSum := 0
	for _, lvl := range u.TrackLevels {
		trackSum += lvl
	}
	base := float64(ub.Cost)*0.35 + float64(trackSum)*4.0
	bounty := int(math.Round(base))
	if bounty < 5 {
		bounty = 5
	}
	return bounty
}

func (s *Sim) removeDead() {
	kept := s.units[:0]
	for _, u := range s.units {
		if u.HP > 0 {
			kept = append(kept, u)
			continue
		}
		s.players[u.Side].UnitsLost++
		if !s.firstBlood {
			s.firstBlood = true
			s.addEvent(SideNone, "firstBlood", "First blood in lane %d!", u.Lane+1)
		}
		if killer := u.LastHitBy; killer == SideA || killer == SideB {
			s.players[killer].Kills++
			if s.Time-s.lastKillAt[killer] <= 3.0 {
				s.streakCount[killer]++
			} else {
				s.streakCount[killer] = 1
			}
			s.lastKillAt[killer] = s.Time

			bounty := s.unitBounty(u)
			streakBonus := math.Min(0.60, float64(s.streakCount[killer]-1)*0.20)
			if streakBonus > 0 {
				bounty = int(math.Round(float64(bounty) * (1.0 + streakBonus)))
			}
			s.players[killer].Coins += bounty
			s.players[killer].BountyEarned += bounty
			s.fx = append(s.fx, FxEvent{Kind: "bounty", Unit: fmt.Sprintf("%d", bounty), Side: killer, Lane: u.Lane, FromX: u.X, ToX: u.X, Slot: -1})

			if n := s.streakCount[killer]; n >= 2 {
				var label string
				switch n {
				case 2:
					label = "DOUBLE KILL"
				case 3:
					label = "TRIPLE KILL"
				default:
					label = fmt.Sprintf("RAMPAGE x%d", n)
				}
				s.addEvent(SideNone, "streak", "%s — %s!", sideName(killer), label)
				s.events[len(s.events)-1].Amount = n
			}
		}
	}
	for i := len(kept); i < len(s.units); i++ {
		s.units[i] = nil
	}
	s.units = kept

	for side := 0; side < 2; side++ {
		for lane := 0; lane < NumLanes; lane++ {
			for slot := 0; slot < TurretSlots; slot++ {
				if t := s.turrets[side][lane][slot]; t != nil && t.HP <= 0 {
					if killer := t.LastHitBy; killer == SideA || killer == SideB {
						s.players[killer].TurretsDown++
						bounty := s.cfg.Turn.TowerKillBounty
						if bounty > 0 {
							s.players[killer].Coins += bounty
							s.players[killer].BountyEarned += bounty
						}
					}
					s.turrets[side][lane][slot] = nil
					s.addEvent(side, "turretDestroyed", "%s tower in lane %d was destroyed! (+%dc)", sideName(side), lane+1, s.cfg.Turn.TowerKillBounty)
				}
			}
		}
	}
}

func (s *Sim) checkEnd() {
	breachA := -1
	breachB := -1
	remaining := s.units[:0]
	for _, u := range s.units {
		if u.HP <= 0 {
			continue
		}
		if u.Side == SideA && u.X >= FieldLength {
			breachA = u.Lane
			s.players[SideB].Lives--
			if s.players[SideB].Lives > 0 && s.cfg.Turn.BreachComebackCoins > 0 {
				s.players[SideB].Coins += s.cfg.Turn.BreachComebackCoins
			}
			continue
		}
		if u.Side == SideB && u.X <= 0 {
			breachB = u.Lane
			s.players[SideA].Lives--
			if s.players[SideA].Lives > 0 && s.cfg.Turn.BreachComebackCoins > 0 {
				s.players[SideA].Coins += s.cfg.Turn.BreachComebackCoins
			}
			continue
		}
		remaining = append(remaining, u)
	}
	s.units = remaining
	if s.players[SideA].Lives < 0 {
		s.players[SideA].Lives = 0
	}
	if s.players[SideB].Lives < 0 {
		s.players[SideB].Lives = 0
	}

	switch {
	case s.players[SideA].Lives == 0 && s.players[SideB].Lives == 0:
		s.Finished = true
		s.Draw = true
		s.Winner = NoSide
		s.addEvent(SideNone, "draw", "Both armies breached on the same tick — draw!")
	case s.players[SideB].Lives == 0:
		s.Finished = true
		s.Winner = SideA
		s.WinLane = breachA
		s.addEvent(SideNone, "breach", "Blue breached in lane %d!", breachA+1)
	case s.players[SideA].Lives == 0:
		s.Finished = true
		s.Winner = SideB
		s.WinLane = breachB
		s.addEvent(SideNone, "breach", "Red breached in lane %d!", breachB+1)
	}
}
