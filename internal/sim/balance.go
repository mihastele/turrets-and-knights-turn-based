// Package sim contains the Lane Siege game simulation.
//
// The simulation is deliberately free of any network or browser code so it can
// be unit-tested headlessly. The server owns one *Sim per match and is the
// only authority for rules, money, damage, turns, and victory.
package sim

// TurretSlots is how many turrets each player may build per lane.
const TurretSlots = 2

// UnitType identifies one of the deployable units.
type UnitType string

const (
	Soldier      UnitType = "soldier"
	Bowman       UnitType = "bowman"
	Knight       UnitType = "knight"
	Shieldbearer UnitType = "shieldbearer"
	Mage         UnitType = "mage"
	Ram          UnitType = "ram"
	Siege        UnitType = "siege"
	Valkyrie     UnitType = "valkyrie"
	Dragon       UnitType = "dragon"
)

// UnitTypes lists every unit in stable order (handy for UI and tests).
// Coupon-locked units are appended last so indices of stock units never shift.
var UnitTypes = []UnitType{Soldier, Bowman, Knight, Shieldbearer, Mage, Ram, Siege, Valkyrie, Dragon}

// UnitClass categorizes units and structures for counter-damage multipliers.
type UnitClass string

const (
	ClassInfantry  UnitClass = "infantry"
	ClassRanged    UnitClass = "ranged"
	ClassCavalry   UnitClass = "cavalry"
	ClassHeavy     UnitClass = "heavy"
	ClassSiege     UnitClass = "siege"
	ClassFlying    UnitClass = "flying"
	ClassStructure UnitClass = "structure"
)

// UpgradeTrack identifies one of the 5 fine-grained upgrade tracks.
type UpgradeTrack string

const (
	TrackDamage      UpgradeTrack = "damage"
	TrackAttackSpeed UpgradeTrack = "attackSpeed"
	TrackHP          UpgradeTrack = "hp"
	TrackShield      UpgradeTrack = "shield"
	TrackSpecial     UpgradeTrack = "special"
)

// UpgradeTracks lists all 5 upgrade tracks in stable UI order.
var UpgradeTracks = []UpgradeTrack{TrackDamage, TrackAttackSpeed, TrackHP, TrackShield, TrackSpecial}

const (
	// NumLanes is the number of lanes on the field.
	NumLanes = 5
	// FieldLength is the length of a lane in abstract units.
	FieldLength = 100.0

	SideA  = 0
	SideB  = 1
	NoSide = -1

	// TickRate is the fixed simulation frequency in Hz.
	TickRate = 20
	// TickDT is the time advanced per tick.
	TickDT = 1.0 / TickRate
	// SnapshotEvery is how many ticks pass between snapshots (2 => 10 Hz).
	SnapshotEvery = 2
)

// UnitBalance holds every tunable number for a unit type.
type UnitBalance struct {
	Name               string                `json:"name"`
	Role               string                `json:"role"`
	Class              UnitClass             `json:"class"`
	Cost               int                   `json:"cost"`
	MaxHP              float64               `json:"maxHp"`
	BaseShield         float64               `json:"baseShield"`
	Damage             float64               `json:"damage"`
	Range              float64               `json:"range"`
	MinRange           float64               `json:"minRange"`
	AttackInterval     float64               `json:"attackInterval"`
	Speed              float64               `json:"speed"`
	UpgradeCosts       [2]int                `json:"upgradeCosts"`
	TrackCosts         map[UpgradeTrack][2]int `json:"trackCosts"`
	UpgradeHP          float64               `json:"upgradeHp"`      // fractional max-HP gain per level
	UpgradeDamage      float64               `json:"upgradeDamage"`  // fractional damage gain per level
	UpgradeSpeed       float64               `json:"upgradeSpeed"`   // fractional attack-rate gain per level
	UpgradeShield      float64               `json:"upgradeShield"`  // fractional base-HP added as shield per level
	VsTurretDamage     float64               `json:"vsTurretDamage"` // optional override; if 0 uses Damage * CounterMultipliers[ClassStructure]
	Splash             float64               `json:"splash,omitempty"`
	Flying             bool                  `json:"flying,omitempty"`
	CanHitFlying       bool                  `json:"canHitFlying,omitempty"`
	ShieldMultiplier   float64               `json:"shieldMultiplier,omitempty"` // >1 deals bonus damage to shields
	ChargeBonus        float64               `json:"chargeBonus,omitempty"`      // first-strike multiplier after moving
	CouponOnly         bool                  `json:"couponOnly,omitempty"`
	CounterMultipliers map[UnitClass]float64 `json:"counterMultipliers,omitempty"`
	SpecialName        string                `json:"specialName,omitempty"`
	SpecialDesc        string                `json:"specialDesc,omitempty"`
}

// TurretBalance holds every tunable number for turrets.
type TurretBalance struct {
	Name                  string                  `json:"name"`
	Cost                  int                     `json:"cost"`
	MaxHP                 float64                 `json:"maxHp"`
	BaseShield            float64                 `json:"baseShield"`
	Damage                float64                 `json:"damage"`
	Range                 float64                 `json:"range"`
	AttackInterval        float64                 `json:"attackInterval"`
	PadX                  float64                 `json:"padX"` // distance of each pad from its owner's edge
	Slots                 int                     `json:"slots"`
	UpgradeCosts          [2]int                  `json:"upgradeCosts"`
	TrackCosts            map[UpgradeTrack][2]int `json:"trackCosts"`
	UpgradeHPPerLevel     float64                 `json:"upgradeHpPerLevel"`
	UpgradeDamagePerLevel float64                 `json:"upgradeDamagePerLevel"`
	SpecialName           string                  `json:"specialName,omitempty"`
	SpecialDesc           string                  `json:"specialDesc,omitempty"`
}

// TurnBalance configures the turn-based Prep/Combat loop.
type TurnBalance struct {
	PrepDuration         float64 `json:"prepDuration"`         // seconds per preparation phase
	CombatDuration       float64 `json:"combatDuration"`       // max seconds per combat round
	MaxRounds            int     `json:"maxRounds"`            // match ends in tie-breaker/draw after MaxRounds
	InitialTowersPerLane int     `json:"initialTowersPerLane"` // pre-built defensive towers in each lane
	EscalationStartRound int     `json:"escalationStartRound"` // round when sudden-death speed/tower-dmg scaling starts
	BreachComebackCoins  int     `json:"breachComebackCoins"`  // emergency coins when losing a life
	BreachBounty         int     `json:"breachBounty"`         // coins awarded to attacker when a unit crosses the enemy line
	TowerKillBounty      int     `json:"towerKillBounty"`      // bounty for destroying an enemy tower
	PillageBonus         int     `json:"pillageBonus"`         // bonus coins per surviving unit past midfield
	ScoutCost            int     `json:"scoutCost"`            // cost to flare a lane during Prep
}

// WallBalance configures buildable, upgradeable, and rebuildable lane reinforcement walls.
type WallBalance struct {
	Name              string  `json:"name"`
	Cost              int     `json:"cost"`
	MaxHP             float64 `json:"maxHp"`
	BaseShield        float64 `json:"baseShield"`
	PadX              float64 `json:"padX"`              // distance from owner's edge
	UpgradeCosts      [2]int  `json:"upgradeCosts"`      // Lv1 and Lv2 upgrade costs
	UpgradeHPPerLevel float64 `json:"upgradeHpPerLevel"` // fractional MaxHP gain per level
	UpgradeShield     float64 `json:"upgradeShield"`     // fractional BaseShield gain per level
	ThornDamage       float64 `json:"thornDamage"`       // melee reflect damage per hit per level
}

// EconomyBalance holds income-related knobs.
type EconomyBalance struct {
	BaseIncome     int     `json:"baseIncome"`     // round 1/base round income
	RoundStep      int     `json:"roundStep"`      // extra coins per round (R-1)*RoundStep
	InterestRate   float64 `json:"interestRate"`   // fraction of unspent coins earned as interest each round
	MaxInterest    int     `json:"maxInterest"`    // cap on round interest
	Interval       float64 `json:"interval"`       // legacy/informational
	WindfallMin    int     `json:"windfallMin"`    // random round windfall bounds
	WindfallMax    int     `json:"windfallMax"`
	FactoryCost    int     `json:"factoryCost"`
	FactoryIncome  int     `json:"factoryIncome"`  // extra coins per round per factory tier
	FactoryPayback float64 `json:"factoryPayback"` // informational
}

// AnomalyBalance configures random lane loots, neutral monsters, and hazards.
type AnomalyBalance struct {
	MinPerRound    int     `json:"minPerRound"`
	MaxPerRound    int     `json:"maxPerRound"`
	CoinAmount     int     `json:"coinAmount"`
	BuffSpeedBonus float64 `json:"buffSpeedBonus"`
	BuffShield     float64 `json:"buffShield"`
	MonsterHP      float64 `json:"monsterHp"`
	MonsterDamage  float64 `json:"monsterDamage"`
	MonsterRange   float64 `json:"monsterRange"`
	MonsterBounty  int     `json:"monsterBounty"`
	HazardDPS      float64 `json:"hazardDps"`
	HazardSlow     float64 `json:"hazardSlow"`
	PickupRadius   float64 `json:"pickupRadius"`
	MudMinRadius   float64 `json:"mudMinRadius"`
	MudMaxRadius   float64 `json:"mudMaxRadius"`
	MudSlow        float64 `json:"mudSlow"`
	SpikeMinRadius float64 `json:"spikeMinRadius"`
	SpikeMaxRadius float64 `json:"spikeMaxRadius"`
	SpikeDPS       float64 `json:"spikeDps"`
}

// FieldEffectBalance configures a coupon-only, lane-targeted field effect.
type FieldEffectBalance struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Role         string  `json:"role"`
	Cost         int     `json:"cost"`
	Cooldown     float64 `json:"cooldown"`     // rounds or seconds between casts per player
	Damage       float64 `json:"damage"`       // rockslide: total per unit; dragonfire: per burn tick
	Duration     float64 `json:"duration"`     // dragonfire / warcry duration in seconds
	TickInterval float64 `json:"tickInterval"` // dragonfire: seconds between burn ticks
}

// CouponBalance holds the coupon-exclusive unlocks tuned here.
type CouponBalance struct {
	Rockslide  FieldEffectBalance `json:"rockslide"`
	DragonFire FieldEffectBalance `json:"dragonFire"`
	Warcry     FieldEffectBalance `json:"warcry"`
	RoundBonus int                `json:"roundBonus"` // small tactical stipend per round for coupon holders
}

// Balance is the single server-side source of truth for tuning.
type Balance struct {
	StartCoins int                      `json:"startCoins"`
	SpawnInset float64                  `json:"spawnInset"` // distance from own edge where units stage
	Units      map[UnitType]UnitBalance `json:"units"`
	Turret     TurretBalance            `json:"turret"`
	Wall       WallBalance              `json:"wall"`
	Turn       TurnBalance              `json:"turn"`
	Economy    EconomyBalance           `json:"economy"`
	Anomaly    AnomalyBalance           `json:"anomaly"`
	Coupon     CouponBalance            `json:"coupon"`
}

func makeTrackCosts(c1, c2 int) map[UpgradeTrack][2]int {
	return map[UpgradeTrack][2]int{
		TrackDamage:      {c1, c2},
		TrackAttackSpeed: {c1, c2},
		TrackHP:          {c1, c2},
		TrackShield:      {c1, c2},
		TrackSpecial:     {c1 + 5, c2 + 10},
	}
}

// DefaultBalance returns the turn-based tactical tuning.
func DefaultBalance() Balance {
	return Balance{
		StartCoins: 110,
		SpawnInset: 3.0,
		Units: map[UnitType]UnitBalance{
			Soldier: {
				Name: "Soldier", Role: "Spearman frontline. Counters Cavalry (+45%) & Siege (+25%).",
				Class: ClassInfantry, Cost: 20, MaxHP: 75, BaseShield: 0, Damage: 10, Range: 2.4, AttackInterval: 1.0, Speed: 5.2,
				UpgradeCosts: [2]int{15, 25}, TrackCosts: makeTrackCosts(12, 22),
				UpgradeHP: 0.22, UpgradeDamage: 0.20, UpgradeSpeed: 0.20, UpgradeShield: 0.25,
				CounterMultipliers: map[UnitClass]float64{
					ClassCavalry:   1.45,
					ClassSiege:     1.25,
					ClassHeavy:     0.75,
					ClassStructure: 0.65,
				},
				SpecialName: "Phalanx Drill",
				SpecialDesc: "+25% vs Cavalry/Heavy & +15 Shield",
			},
			Bowman: {
				Name: "Bowman", Role: "Anti-air & ranged support. Shreds Flying (+65%) & Infantry (+25%).",
				Class: ClassRanged, Cost: 30, MaxHP: 48, BaseShield: 0, Damage: 13, Range: 16.0, AttackInterval: 1.15, Speed: 4.8,
				UpgradeCosts: [2]int{18, 30}, TrackCosts: makeTrackCosts(15, 26),
				UpgradeHP: 0.22, UpgradeDamage: 0.20, UpgradeSpeed: 0.20, UpgradeShield: 0.25,
				CanHitFlying: true,
				CounterMultipliers: map[UnitClass]float64{
					ClassFlying:    1.65,
					ClassInfantry:  1.25,
					ClassHeavy:     0.55,
					ClassStructure: 0.50,
				},
				SpecialName: "Incendiary Arrows",
				SpecialDesc: "Arrows burn targets & deal +25% vs Structures",
			},
			Knight: {
				Name: "Knight", Role: "Shock cavalry. Charges Ranged & Siege (+50%). Weak to Spears.",
				Class: ClassCavalry, Cost: 45, MaxHP: 100, BaseShield: 0, Damage: 16, Range: 2.5, AttackInterval: 0.9, Speed: 8.5,
				UpgradeCosts: [2]int{22, 38}, TrackCosts: makeTrackCosts(18, 32),
				UpgradeHP: 0.22, UpgradeDamage: 0.20, UpgradeSpeed: 0.20, UpgradeShield: 0.25,
				ChargeBonus: 1.60,
				CounterMultipliers: map[UnitClass]float64{
					ClassRanged:    1.50,
					ClassSiege:     1.50,
					ClassInfantry:  0.75,
					ClassHeavy:     0.70,
					ClassStructure: 0.80,
				},
				SpecialName: "Thunderous Lance",
				SpecialDesc: "Charge resets on kill & deals +20% burst",
			},
			Shieldbearer: {
				Name: "Shieldbearer", Role: "Armored vanguard with 45 Shield. Resists arrows & towers.",
				Class: ClassHeavy, Cost: 50, MaxHP: 130, BaseShield: 45, Damage: 11, Range: 2.4, AttackInterval: 1.1, Speed: 4.2,
				UpgradeCosts: [2]int{24, 40}, TrackCosts: makeTrackCosts(20, 34),
				UpgradeHP: 0.22, UpgradeDamage: 0.20, UpgradeSpeed: 0.20, UpgradeShield: 0.30,
				CounterMultipliers: map[UnitClass]float64{
					ClassCavalry:   1.35,
					ClassInfantry:  1.15,
					ClassStructure: 0.85,
				},
				SpecialName: "Reflective Bulwark",
				SpecialDesc: "Reflects 25% of incoming damage back to attacker",
			},
			Mage: {
				Name: "Arcane Mage", Role: "Splash caster. Shreds Shields (2x) & Heavy (+60%). Hits air.",
				Class: ClassRanged, Cost: 60, MaxHP: 55, BaseShield: 20, Damage: 18, Range: 13.5, AttackInterval: 1.45, Speed: 4.4,
				UpgradeCosts: [2]int{26, 44}, TrackCosts: makeTrackCosts(22, 36),
				UpgradeHP: 0.22, UpgradeDamage: 0.20, UpgradeSpeed: 0.20, UpgradeShield: 0.25,
				Splash: 1.8, CanHitFlying: true, ShieldMultiplier: 2.0,
				CounterMultipliers: map[UnitClass]float64{
					ClassHeavy:     1.60,
					ClassFlying:    1.30,
					ClassCavalry:   0.65,
					ClassStructure: 0.75,
				},
				SpecialName: "Arcane Overcharge",
				SpecialDesc: "+50% splash radius & +20% spell damage",
			},
			Ram: {
				Name: "Battering Ram", Role: "Heavy tower-breaker (3.8x vs Structures). Needs escort.",
				Class: ClassSiege, Cost: 55, MaxHP: 180, BaseShield: 30, Damage: 9, Range: 2.6, AttackInterval: 1.2, Speed: 3.3,
				UpgradeCosts: [2]int{25, 42}, TrackCosts: makeTrackCosts(20, 34),
				UpgradeHP: 0.22, UpgradeDamage: 0.20, UpgradeSpeed: 0.20, UpgradeShield: 0.25,
				VsTurretDamage: 34,
				CounterMultipliers: map[UnitClass]float64{
					ClassStructure: 3.80,
					ClassInfantry:  0.60,
					ClassCavalry:   0.60,
					ClassRanged:    0.60,
					ClassHeavy:     0.60,
				},
				SpecialName: "Ironclad Ram",
				SpecialDesc: "+40% Structure damage & +50 Shield",
			},
			Siege: {
				Name: "Siege Engine", Role: "Long-range splash artillery. Outranges towers; blasts clustered foes.",
				Class: ClassSiege, Cost: 75, MaxHP: 85, BaseShield: 0, Damage: 28, Range: 28.0, MinRange: 11.0, AttackInterval: 2.3, Speed: 2.5,
				UpgradeCosts: [2]int{32, 55}, TrackCosts: makeTrackCosts(26, 42),
				UpgradeHP: 0.22, UpgradeDamage: 0.20, UpgradeSpeed: 0.20, UpgradeShield: 0.25,
				Splash: 2.2, VsTurretDamage: 42,
				CounterMultipliers: map[UnitClass]float64{
					ClassStructure: 1.50,
					ClassHeavy:     1.20,
				},
				SpecialName: "Shrapnel Shells",
				SpecialDesc: "+35% damage and splash radius",
			},
			Valkyrie: {
				Name: "Valkyrie", Role: "Coupon unlock. Swift flying striker; hits air/ground & buffs allies.",
				Class: ClassFlying, Cost: 110, MaxHP: 155, BaseShield: 40, Damage: 22, Range: 3.8, AttackInterval: 1.05, Speed: 7.2,
				UpgradeCosts: [2]int{40, 70}, TrackCosts: makeTrackCosts(30, 50),
				UpgradeHP: 0.22, UpgradeDamage: 0.20, UpgradeSpeed: 0.20, UpgradeShield: 0.25,
				Flying: true, CanHitFlying: true, CouponOnly: true,
				CounterMultipliers: map[UnitClass]float64{
					ClassFlying:    1.50,
					ClassCavalry:   1.35,
					ClassRanged:    0.85,
					ClassStructure: 0.90,
				},
				SpecialName: "Valkyrie Aura",
				SpecialDesc: "+20% all combat stats & +30 Shield",
			},
			Dragon: {
				Name: "Dragon", Role: "Coupon unlock. Flying fire-breather with heavy splash.",
				Class: ClassFlying, Cost: 140, MaxHP: 240, BaseShield: 50, Damage: 26, Range: 5.0, AttackInterval: 1.25, Speed: 5.8,
				UpgradeCosts: [2]int{50, 85}, TrackCosts: makeTrackCosts(36, 60),
				UpgradeHP: 0.22, UpgradeDamage: 0.20, UpgradeSpeed: 0.20, UpgradeShield: 0.25,
				Splash: 2.0, Flying: true, CanHitFlying: true, CouponOnly: true,
				CounterMultipliers: map[UnitClass]float64{
					ClassInfantry:  1.35,
					ClassHeavy:     1.35,
					ClassStructure: 1.15,
				},
				SpecialName: "Elder Bloodline",
				SpecialDesc: "+20% all combat stats & +40 Shield",
			},
		},
		Turret: TurretBalance{
			Name: "Protective Tower", Cost: 65, MaxHP: 150, BaseShield: 30, Damage: 15, Range: 14.0, AttackInterval: 1.05,
			PadX:                  10,
			Slots:                 TurretSlots,
			UpgradeCosts:          [2]int{25, 45},
			TrackCosts:            makeTrackCosts(20, 35),
			UpgradeHPPerLevel:     0.25,
			UpgradeDamagePerLevel: 0.22,
			SpecialName:           "Flak & Focus Coils",
			SpecialDesc:           "Allows tower to target Flying units & +25% attack speed",
		},
		Wall: WallBalance{
			Name:              "Reinforcement Wall",
			Cost:              45,
			MaxHP:             280,
			BaseShield:        80,
			PadX:              22,
			UpgradeCosts:      [2]int{30, 50},
			UpgradeHPPerLevel: 0.40,
			UpgradeShield:     0.50,
			ThornDamage:       4.0,
		},
		Turn: TurnBalance{
			PrepDuration:         30,
			CombatDuration:       22,
			MaxRounds:            10,
			InitialTowersPerLane: 1,
			EscalationStartRound: 5,
			BreachComebackCoins:  30,
			BreachBounty:         25,
			TowerKillBounty:      35,
			PillageBonus:         5,
			ScoutCost:            15,
		},
		Economy: EconomyBalance{
			BaseIncome:     35,
			RoundStep:      8,
			InterestRate:   0.10,
			MaxInterest:    25,
			Interval:       5,
			WindfallMin:    5,
			WindfallMax:    15,
			FactoryCost:    55,
			FactoryIncome:  15,
			FactoryPayback: 4,
		},
		Anomaly: AnomalyBalance{
			MinPerRound:    2,
			MaxPerRound:    3,
			CoinAmount:     35,
			BuffSpeedBonus: 0.30,
			BuffShield:     35,
			MonsterHP:      160,
			MonsterDamage:  15,
			MonsterRange:   4.0,
			MonsterBounty:  50,
			HazardDPS:      6.0,
			HazardSlow:     0.35,
			PickupRadius:   3.2,
			MudMinRadius:   3.5,
			MudMaxRadius:   8.5,
			MudSlow:        0.45,
			SpikeMinRadius: 4.0,
			SpikeMaxRadius: 7.0,
			SpikeDPS:       9.0,
		},
		Coupon: CouponBalance{
			RoundBonus: 10,
			Rockslide: FieldEffectBalance{
				ID: "rockslide", Name: "Rockslide",
				Role:     "Coupon unlock. A rockfall crushes every ground enemy in the lane.",
				Cost:     45,
				Cooldown: 18,
				Damage:   65,
			},
			DragonFire: FieldEffectBalance{
				ID: "dragonfire", Name: "Dragon Fire",
				Role:         "Coupon unlock. Sets the lane ablaze — burning ground sears everything inside.",
				Cost:         70,
				Cooldown:     25,
				Damage:       8,
				Duration:     6,
				TickInterval: 0.5,
			},
			Warcry: FieldEffectBalance{
				ID: "warcry", Name: "Warcry Blessing",
				Role:     "Coupon unlock. Grants +35% attack speed & +30 Shield to your units in a lane.",
				Cost:     40,
				Cooldown: 20,
				Damage:   30, // shield grant
				Duration: 8,
			},
		},
	}
}
