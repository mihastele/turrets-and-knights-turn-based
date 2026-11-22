/* Lane Siege — browser client. Talks to the Go server over WebSocket.
 * The server is authoritative; this file renders state, plays juice, and
 * sends intentions. */
'use strict';

// ---------------------------------------------------------------------------
// Canvas setup
// ---------------------------------------------------------------------------
const FIELD = { x0: 52, x1: 1108, y0: 14, lanes: 5, laneH: 116 };
const W = 1160, H = 620;
const SCALE = 2;
const UNIT_TYPES = ['soldier', 'bowman', 'knight', 'shieldbearer', 'mage', 'ram', 'siege'];
const COUPON_UNITS = ['valkyrie', 'dragon'];
const ALL_UNITS = [...UNIT_TYPES, ...COUPON_UNITS];
const DRAGON_TYPE = 'dragon';
const ABILITY_EFFECTS = ['rockslide', 'dragonfire', 'warcry'];
const TRACKS = [
  { id: 'damage', label: '⚔️ Attack' },
  { id: 'attackSpeed', label: '⚡ Speed' },
  { id: 'hp', label: '❤️ Vitality' },
  { id: 'shield', label: '🛡️ Shield' },
  { id: 'special', label: '👑 Elite', couponOnly: true },
];
const HOTKEYS = { '1': 'soldier', '2': 'bowman', '3': 'knight', '4': 'shieldbearer', '5': 'mage', '6': 'ram', '7': 'siege', 't': 'turret', 'w': 'wall', 's': 'scout', 'f': 'factory' };
const COUPON_HOTKEYS = { '8': 'valkyrie', '9': 'dragon', '0': 'rockslide', '-': 'dragonfire', '=': 'warcry' };
const LANE_H = FIELD.laneH;

const cv = document.getElementById('battlefield');
const ctx = cv.getContext('2d');
cv.width = W * SCALE;
cv.height = H * SCALE;
ctx.scale(SCALE, SCALE);

const confettiCv = document.getElementById('confetti');
const fctx = confettiCv.getContext('2d');

const laneY = (lane) => FIELD.y0 + lane * LANE_H + LANE_H / 2;
const unitY = (u) => laneY(u.lane) + (u.yOff || 0) * LANE_H;
// Vertical offset of each turret slot within its lane (stacked pair, same x).
const TURRET_SLOT_OFF = [-0.21, 0.21];
const turretSlotY = (lane, slot) => laneY(lane) + (TURRET_SLOT_OFF[slot] || 0) * LANE_H;
const fieldX = (x) => FIELD.x0 + (x / 100) * (FIELD.x1 - FIELD.x0);

// ---------------------------------------------------------------------------
// Audio (Web Audio, no assets, mutable)
// ---------------------------------------------------------------------------
const SFX = (() => {
  let ctxA = null;
  let master = null;
  let muted = false;
  try { muted = localStorage.getItem('laneSiegeMuted') === '1'; } catch {}

  function ensure() {
    if (!ctxA) {
      try {
        ctxA = new (window.AudioContext || window.webkitAudioContext)();
        master = ctxA.createGain();
        master.gain.value = 0.5;
        master.connect(ctxA.destination);
      } catch { return null; }
    }
    if (ctxA.state === 'suspended') ctxA.resume().catch(() => {});
    return ctxA;
  }
  function env(t0, a, peak, d) {
    const g = ctxA.createGain();
    g.gain.setValueAtTime(0.0001, t0);
    g.gain.linearRampToValueAtTime(peak, t0 + a);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + a + d);
    return g;
  }
  function tone({ type = 'sine', f = 440, f1 = null, t = 0.15, vol = 0.2, delay = 0 }) {
    if (muted) return;
    const c = ensure(); if (!c) return;
    const t0 = c.currentTime + delay;
    const o = c.createOscillator();
    o.type = type;
    o.frequency.setValueAtTime(f, t0);
    if (f1) o.frequency.exponentialRampToValueAtTime(Math.max(1, f1), t0 + t);
    const g = env(t0, 0.005, vol, t);
    o.connect(g).connect(master);
    o.start(t0); o.stop(t0 + t + 0.05);
  }
  function noise({ t = 0.25, vol = 0.25, cutoff = 1200, delay = 0 }) {
    if (muted) return;
    const c = ensure(); if (!c) return;
    const t0 = c.currentTime + delay;
    const len = Math.max(1, Math.floor(c.sampleRate * t));
    const buf = c.createBuffer(1, len, c.sampleRate);
    const data = buf.getChannelData(0);
    for (let i = 0; i < len; i++) data[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = c.createBufferSource();
    src.buffer = buf;
    const filter = c.createBiquadFilter();
    filter.type = 'lowpass';
    filter.frequency.value = cutoff;
    const g = env(t0, 0.004, vol, t);
    src.connect(filter).connect(g).connect(master);
    src.start(t0);
  }

  // ---- Procedural war-drums soundtrack -----------------------------
  // A slow minor-key ostinato. Subtle in the mix; goes double-time in overtime.
  let musicGain = null, musicTimer = null, musicStep = 0, nextNoteAt = 0, musicHot = false;
  const BASSLINE = [55, 0, 55, 0, 65.41, 0, 49.0, 0, 55, 0, 55, 0, 73.42, 65.41, 58.27, 0]; // A A C F A A D C#-ish (A minor)
  function mNote({ type, f, t, dur, vol, slideTo }) {
    if (!musicGain) return;
    const o = ctxA.createOscillator();
    o.type = type;
    o.frequency.setValueAtTime(f, t);
    if (slideTo) o.frequency.exponentialRampToValueAtTime(slideTo, t + dur);
    const g = ctxA.createGain();
    g.gain.setValueAtTime(0.0001, t);
    g.gain.linearRampToValueAtTime(vol, t + 0.008);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    o.connect(g).connect(musicGain);
    o.start(t); o.stop(t + dur + 0.02);
  }
  function mNoise(t, dur, vol, type, freq) {
    if (!musicGain) return;
    const len = Math.max(1, Math.floor(ctxA.sampleRate * dur));
    const buf = ctxA.createBuffer(1, len, ctxA.sampleRate);
    const d = buf.getChannelData(0);
    for (let i = 0; i < len; i++) d[i] = (Math.random() * 2 - 1) * (1 - i / len);
    const src = ctxA.createBufferSource();
    src.buffer = buf;
    const f = ctxA.createBiquadFilter();
    f.type = type; f.frequency.value = freq;
    const g = ctxA.createGain();
    g.gain.setValueAtTime(vol, t);
    g.gain.exponentialRampToValueAtTime(0.0001, t + dur);
    src.connect(f).connect(g).connect(musicGain);
    src.start(t);
  }
  function scheduleMusic() {
    const c = ctxA;
    if (!c || !musicGain) return;
    const eighth = 60 / (musicHot ? 118 : 96) / 2;
    while (nextNoteAt < c.currentTime + 0.4) {
      const s = musicStep % 16;
      if (muted) { // keep the sequencer in step, emit silence
        musicStep++;
        nextNoteAt += eighth;
        continue;
      }
      const t = Math.max(nextNoteAt, c.currentTime + 0.01);
      if (s % 4 === 0) mNote({ type: 'sine', f: 120, slideTo: 42, t, dur: 0.16, vol: 0.5 });           // kick
      if (s === 4 || s === 12) mNoise(t, 0.09, 0.22, 'bandpass', 1800);                                // snare
      if (s % 2 === 0) mNoise(t, 0.03, 0.06, 'highpass', 7000);                                        // hat
      if (musicHot && s % 4 === 2) mNote({ type: 'sine', f: 110, slideTo: 40, t, dur: 0.12, vol: 0.35 }); // extra kick in overtime
      const bass = BASSLINE[s];
      if (bass) mNote({ type: 'sawtooth', f: bass, t, dur: eighth * 0.9, vol: 0.16 });
      musicStep++;
      nextNoteAt += eighth;
    }
  }
  let lastHit = 0;
  function startMusic() {
    const c = ensure(); if (!c) return;
    stopMusic();
    musicGain = c.createGain();
    musicGain.gain.value = 0.5;
    musicGain.connect(master);
    musicStep = 0;
    nextNoteAt = c.currentTime + 0.06;
    musicTimer = setInterval(scheduleMusic, 90);
  }
  function stopMusic() {
    if (musicTimer) clearInterval(musicTimer);
    musicTimer = null;
    if (musicGain) { try { musicGain.disconnect(); } catch {} }
    musicGain = null;
  }
  return {
    unlock() { ensure(); },
    startMusic,
    stopMusic,
    setMusicHot(hot) { musicHot = !!hot; },
    toggleMute() {
      muted = !muted;
      try { localStorage.setItem('laneSiegeMuted', muted ? '1' : '0'); } catch {}
      return muted;
    },
    isMuted: () => muted,
    click()  { tone({ type: 'square', f: 760, t: 0.04, vol: 0.05 }); },
    deploy() { tone({ type: 'triangle', f: 180, f1: 110, t: 0.14, vol: 0.28 }); tone({ type: 'sine', f: 520, t: 0.06, vol: 0.08, delay: 0.02 }); },
    build()  { tone({ type: 'square', f: 240, t: 0.07, vol: 0.16 }); tone({ type: 'square', f: 170, t: 0.09, vol: 0.16, delay: 0.07 }); },
    upgrade(){ tone({ type: 'triangle', f: 523, t: 0.07, vol: 0.18 }); tone({ type: 'triangle', f: 784, t: 0.09, vol: 0.18, delay: 0.06 }); },
    error()  { tone({ type: 'sawtooth', f: 140, f1: 90, t: 0.18, vol: 0.16 }); },
    arrow()  { tone({ type: 'sawtooth', f: 900 + Math.random() * 200, f1: 420, t: 0.05, vol: 0.07 }); },
    turretShot() { tone({ type: 'square', f: 500 + Math.random() * 120, t: 0.05, vol: 0.12 }); },
    shell()  { tone({ type: 'sine', f: 120, f1: 42, t: 0.45, vol: 0.5 }); noise({ t: 0.3, vol: 0.28, cutoff: 500 }); },
    hit() {
      const n = performance.now();
      if (n - lastHit < 70) return;
      lastHit = n;
      tone({ type: 'triangle', f: 320, f1: 170, t: 0.05, vol: 0.14 });
    },
    poof()   { tone({ type: 'sine', f: 430, f1: 120, t: 0.16, vol: 0.2 }); noise({ t: 0.08, vol: 0.1, cutoff: 900 }); },
    boom()   { noise({ t: 0.45, vol: 0.4, cutoff: 700 }); tone({ type: 'sine', f: 82, f1: 34, t: 0.5, vol: 0.5 }); },
    rockfall() { noise({ t: 0.7, vol: 0.5, cutoff: 380 }); tone({ type: 'sine', f: 58, f1: 29, t: 0.7, vol: 0.5 }); noise({ t: 0.3, vol: 0.3, cutoff: 250, delay: 0.12 }); },
    inferno()  { tone({ type: 'sawtooth', f: 160, f1: 430, t: 0.55, vol: 0.28 }); noise({ t: 0.7, vol: 0.35, cutoff: 950 }); },
    fire()     { noise({ t: 0.13, vol: 0.13, cutoff: 1500 }); tone({ type: 'sawtooth', f: 340, f1: 190, t: 0.12, vol: 0.1 }); },
    cacheDrop() { tone({ type: 'sine', f: 90, f1: 46, t: 0.35, vol: 0.4 }); tone({ type: 'triangle', f: 1320, t: 0.08, vol: 0.15, delay: 0.18 }); },
    streakHorn() { tone({ type: 'sawtooth', f: 294, t: 0.14, vol: 0.3 }); tone({ type: 'sawtooth', f: 392, t: 0.22, vol: 0.3, delay: 0.12 }); },
    windfall() { [659, 784, 988].forEach((f, i) => tone({ type: 'triangle', f, t: 0.09, vol: 0.22, delay: i * 0.07 })); },
    firstBlood() { tone({ type: 'sawtooth', f: 196, f1: 147, t: 0.45, vol: 0.3 }); },
    breach() { [147, 147, 196].forEach((f, i) => tone({ type: 'sawtooth', f, t: 0.28, vol: 0.35, delay: i * 0.22 })); },
    win()    { [523, 659, 784, 1047].forEach((f, i) => tone({ type: 'triangle', f, t: 0.22, vol: 0.3, delay: i * 0.13 })); },
    lose()   { [392, 330, 262].forEach((f, i) => tone({ type: 'triangle', f, t: 0.3, vol: 0.3, delay: i * 0.18 })); },
  };
})();

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------
const state = {
  screen: 'lobby',
  ws: null,
  seat: null,
  code: null,
  balance: null,
  coupon: null, // { entitled, code, label, expiresAt } from the server hello/status
  snapNow: null,
  snapPrev: null,
  snapNowAt: 0,
  snapPrevAt: 0,
  selection: null,
  hoverLane: null,
  rematchPending: false,
  feed: [],
  over: false,
  hoveredEntity: null,
  selectedUpgradeUnit: 'soldier',
};

// FX pools (coordinates: x in field units, y in screen px offset from lane center)
const fx = {
  projectiles: [],
  particles: [],
  floaters: [],
  flashes: [],       // {x, y, r, until, color}
  rings: [],         // expanding shockwaves {x, y, r, maxR, life, maxLife}
  hpCache: new Map(),// key -> {hp, x, lane, yOff, side, kind}
  shake: 0,
  lastAlarmAt: 0,
};

const $ = (id) => document.getElementById(id);
// Compact (phone/tablet) mode: sidebar becomes a drawer.
const mqCompact = window.matchMedia('(max-width: 1024px), (max-height: 560px)');
const coarsePointer = window.matchMedia('(pointer: coarse)').matches;
const isCompact = () => mqCompact.matches;
function setSidebar(open) {
  const willOpen = open === undefined ? !els.sidebar.classList.contains('open') : open;
  els.sidebar.classList.toggle('open', willOpen);
  els.sidebarBackdrop.classList.toggle('hidden', !willOpen);
  if (willOpen) els.shopToggle.classList.remove('pulse');
}
const els = {
  lobby: $('screen-lobby'), game: $('screen-game'),
  lobbyError: $('lobby-error'),
  roomCode: $('room-code'), waitingCode: $('waiting-code'),
  waiting: $('waiting-overlay'), gameover: $('gameover-overlay'),
  resultTitle: $('result-title'), resultDetail: $('result-detail'),
  rematchBtn: $('btn-rematch'), rematchStatus: $('rematch-status'),
  resignBtn: $('btn-resign'),
  clock: $('clock'), phase: $('phase-pill'), turretPower: $('turret-power'),
  roundBadge: $('round-badge'), btnReady: $('btn-ready'), readyStatus: $('ready-status'),
  poolBar: $('pool-bar'), upgradeUnitTabs: $('upgrade-unit-tabs'),
  sideBadge: $('side-badge'),
  coinsYou: $('coins-you'), coinsFoe: $('coins-foe'),
  incomeYou: $('income-you'), incomeFoe: $('income-foe'),
  coinChip: $('coin-chip-you'),
  shop: $('shop'), unitUpgrades: $('unit-upgrades'), turretUpgrades: $('turret-upgrades'),
  feed: $('feed'), toast: $('toast'),
  disconnect: $('disconnect-banner'),
  selHint: $('selection-hint'),
  windfall: $('windfall-banner'),
  breachWarn: $('breach-warning'),
  muteBtn: $('btn-mute'),
  shopToggle: $('shop-toggle'),
  sidebar: $('sidebar'),
  sidebarBackdrop: $('sidebar-backdrop'),
  sidebarClose: $('sidebar-close'),
  rotateDismiss: $('rotate-dismiss'),
  emoteBar: $('emote-bar'),
  matchStats: $('match-stats'),
  couponBadge: $('coupon-badge'),
  couponPanel: $('coupon-panel'),
  couponExpiry: $('coupon-expiry'),
  couponForm: $('coupon-form'),
  couponCode: $('coupon-code'),
  couponStatus: $('coupon-status'),
  abilityRockslide: $('ability-rockslide'),
  abilityDragonfire: $('ability-dragonfire'),
  abilityWarcry: $('ability-warcry'),
};

// ---------------------------------------------------------------------------
// Session persistence
// ---------------------------------------------------------------------------
const SS = {
  save(code, token, seat) {
    try { sessionStorage.setItem('laneSiege', JSON.stringify({ code, token, seat })); } catch {}
  },
  load() {
    try { return JSON.parse(sessionStorage.getItem('laneSiege') || 'null'); } catch { return null; }
  },
  clear() { try { sessionStorage.removeItem('laneSiege'); } catch {} },
};
let sessionToken = null;

//-------------------------------------------------------------------------------
// Coupons (Dragon unlocks). The player id is a stable random id in
// localStorage; redeemed codes are kept there too and sent up with every
// create/join/rejoin so the server can re-validate them (they may expire or
// be revoked at any time).
//-------------------------------------------------------------------------------
const PID_KEY = 'laneSiegePlayerId';
const COUPON_KEY = 'laneSiegeCoupons';

function playerId() {
  try {
    let id = localStorage.getItem(PID_KEY);
    if (!id) {
      id = (crypto.randomUUID ? crypto.randomUUID() : 'p-' + Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 10));
      localStorage.setItem(PID_KEY, id);
    }
    return id;
  } catch {
    return 'anon';
  }
}

const CouponStore = {
  load() {
    try { const v = JSON.parse(localStorage.getItem(COUPON_KEY) || '[]'); return Array.isArray(v) ? v : []; } catch { return []; }
  },
  add(code) {
    const list = this.load();
    if (!list.includes(code)) {
      list.push(code);
      try { localStorage.setItem(COUPON_KEY, JSON.stringify(list)); } catch {}
    }
  },
  clear() { try { localStorage.removeItem(COUPON_KEY); } catch {} },
  remove(code) {
    const normalized = code.replace(/[ -]/g, '').toUpperCase();
    const remaining = this.load().filter(c => c.replace(/[ -]/g, '').toUpperCase() !== normalized);
    try { if (remaining.length) localStorage.setItem(COUPON_KEY, JSON.stringify(remaining)); else this.clear(); } catch {}
  },
};

function updateCouponRemoveButton() {
  $('coupon-remove').classList.toggle('hidden', CouponStore.load().length === 0);
}

function couponPayload() {
  return { playerId: playerId(), coupons: CouponStore.load() };
}

// ---------------------------------------------------------------------------
// Lobby: coupon redemption
// ---------------------------------------------------------------------------
function fmtExpiry(iso) {
  const d = new Date(iso);
  return isNaN(d) ? '' : d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

async function refreshCouponStatus() {
  updateCouponRemoveButton();
  if (state.coupon && state.coupon.expiresAt && Date.now() >= Date.parse(state.coupon.expiresAt)) applyCouponFromHello(null);
  const codes = CouponStore.load();
  if (!codes.length) {
    if (els.couponStatus) els.couponStatus.textContent = '';
    return;
  }
  try {
    const res = await api('/api/coupons/status', { codes });
    const dead = new Set((res.coupons || []).filter(c => !c.valid).map(c => c.code.replace(/[ -]/g, '').toUpperCase()));
    const kept = CouponStore.load().filter(c => !dead.has(c.replace(/[ -]/g, '').toUpperCase()));
    try { localStorage.setItem(COUPON_KEY, JSON.stringify(kept)); } catch {}
    updateCouponRemoveButton();
    if (state.coupon && (res.coupons || []).some(c => c.reason !== 'exhausted' && !c.valid && c.code.replace(/[ -]/g, '').toUpperCase() === state.coupon.code.replace(/[ -]/g, '').toUpperCase())) applyCouponFromHello(null);
    if (!CouponStore.load().length && !dead.size) { els.couponStatus.textContent = ''; return; }
    if (res.entitled && res.active) {
      els.couponStatus.textContent = `🐉 ${res.active.label || 'Coupon'} unlocked — Dragon + field effects · until ${fmtExpiry(res.active.expiresAt)}`;
      els.couponStatus.className = 'coupon-status ok';
    } else {
      // Prune dead codes so a fresh redeem isn't blocked by stale entries.
      const first = (res.coupons || [])[0];
      const why = first && first.reason ? first.reason : 'unknown';
      els.couponStatus.textContent = `Coupon not active (${why}). Ask your admin for a new one.`;
      els.couponStatus.className = 'coupon-status err';
    }
  } catch {
    // Offline/server hiccup — keep whatever we have, stay quiet.
  }
}

async function onRedeemCoupon(ev) {
  ev.preventDefault();
  const raw = els.couponCode.value.trim();
  if (!raw) return;
  els.couponStatus.textContent = 'Checking coupon…';
  els.couponStatus.className = 'coupon-status';
  SFX.click();
  try {
    const res = await api('/api/coupons/redeem', { code: raw, playerId: playerId() });
    if (res.coupon && res.coupon.code) CouponStore.add(res.coupon.code);
    els.couponCode.value = '';
    SFX.windfall();
    await refreshCouponStatus();
  } catch (e) {
    els.couponStatus.textContent = e.message;
    els.couponStatus.className = 'coupon-status err';
    SFX.error();
  }
}

// ---------------------------------------------------------------------------
// Coupon unlocks in-match (Dragon shop item, upgrade row, ability panel)
// ---------------------------------------------------------------------------
function applyCouponFromHello(coupon) {
  state.coupon = coupon && coupon.entitled ? coupon : null;
  const on = !!state.coupon;
  els.couponBadge.classList.toggle('hidden', !on);
  addCouponShopItems();
  buildUpgradePanels();
  els.couponPanel.classList.toggle('locked', !on);
  els.couponExpiry.textContent = on ? '' : '🔒 Requires an active coupon · see the coupon guide in the lobby';
  els.couponPanel.querySelectorAll('.ability-btn').forEach(btn => {
    btn.disabled = !on;
    btn.title = on ? '' : '🔒 Requires an active coupon';
  });
  const cb = state.balance && state.balance.coupon;
  if (cb) {
    $('ability-rockslide-cost').textContent = cb.rockslide.cost + 'c';
    $('ability-dragonfire-cost').textContent = cb.dragonFire.cost + 'c';
    if (cb.warcry) $('ability-warcry-cost').textContent = cb.warcry.cost + 'c';
  }
  if (!on) return;
  const label = state.coupon.label || 'Coupon holder';
  const until = state.coupon.expiresAt ? `valid until ${fmtExpiry(state.coupon.expiresAt)}` : '';
  els.couponBadge.title = `${label}${until ? ' — ' + until : ''}`;
  els.couponExpiry.textContent = `${label}${until ? ' — ' + until : ''}`;
}

function addCouponShopItems() {
  const specs = [
    { unit: 'valkyrie', key: '8', icon: '⚡' },
    { unit: 'dragon', key: '9', icon: '🐉' },
  ];
  for (const s of specs) {
    const existing = els.shop.querySelector(`.shop-item[data-unit="${s.unit}"]`);
    const u = state.balance && state.balance.units && state.balance.units[s.unit];
    if (!u) continue;
    if (existing) {
      existing.classList.toggle('locked-coupon', !state.coupon);
      const lockSpan = existing.querySelector('.lock-tag');
      if (lockSpan) lockSpan.textContent = state.coupon ? '✓ Coupon' : '🔒 Coupon';
      continue;
    }
    const btn = document.createElement('button');
    btn.className = 'shop-item dragon-item' + (state.coupon ? '' : ' locked-coupon');
    btn.dataset.unit = s.unit;
    btn.dataset.cost = u.cost;
    btn.innerHTML =
      `<span class="si-key">${s.key}</span>` +
      `<div class="si-top"><span class="si-name">${s.icon} ${u.name}</span><span class="si-cost">${u.cost}c</span></div>` +
      `<div class="si-role">${u.role} <em class="lock-tag">${state.coupon ? '✓ Coupon' : '🔒 Coupon'}</em></div>`;
    btn.addEventListener('click', () => { SFX.click(); selectUnit(s.unit); });
    els.shop.appendChild(btn);
  }
}

function selectAbility(effect) {
  if (coarsePointer && state.selection && state.selection.kind === 'ability' && state.selection.effect === effect) { clearSelection(); return; }
  state.selection = { kind: 'ability', effect };
  highlightSelection();
  if (isCompact()) setSidebar(false);
}

function abilityButtonFor(effect) {
  if (effect === 'rockslide') return els.abilityRockslide;
  if (effect === 'dragonfire') return els.abilityDragonfire;
  return els.abilityWarcry;
}

function updateAbilityButtons(snap) {
  const b = state.balance && state.balance.coupon;
  const cfgFor = (e) => {
    if (!b) return null;
    if (e === 'rockslide') return b.rockslide;
    if (e === 'dragonfire') return b.dragonFire;
    return b.warcry;
  };
  const you = snap.players[state.seat];
  const abilities = (you && you.abilities) || {};
  for (const effect of ABILITY_EFFECTS) {
    const btn = els.couponPanel.querySelector(`[data-effect="${effect}"]`);
    if (!btn) continue;
    const cdEl = btn.querySelector('.ab-cooldown');
    if (!state.coupon) {
      btn.disabled = true;
      cdEl.textContent = '🔒 Coupon';
      cdEl.classList.remove('ready');
      continue;
    }
    const cfg = cfgFor(effect);
    const cost = cfg ? cfg.cost : 0;
    const cd = abilities[effect] || 0;
    btn.disabled = state.over || cd > 0.05 || !you || you.coins < cost;
    if (cd > 0.05) {
      cdEl.textContent = `Cooldown ${Math.ceil(cd)}s`;
      cdEl.classList.remove('ready');
    } else {
      cdEl.textContent = 'Ready';
      cdEl.classList.add('ready');
    }
  }
}

// ---------------------------------------------------------------------------
// Networking
// ---------------------------------------------------------------------------
async function api(path, body) {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body ? JSON.stringify(body) : '{}',
  });
  let data = null;
  try { data = await res.json(); } catch { /* ignore */ }
  if (!res.ok) throw new Error((data && data.error) || `Request failed (${res.status})`);
  return data;
}

function connectWS(code, token) {
  const proto = location.protocol === 'https:' ? 'wss' : 'ws';
  const ws = new WebSocket(`${proto}://${location.host}/ws?room=${encodeURIComponent(code)}&token=${encodeURIComponent(token)}`);
  state.ws = ws;

  ws.onmessage = (ev) => {
    let msg;
    try { msg = JSON.parse(ev.data); } catch { return; }
    handleServerMessage(msg);
  };
  ws.onclose = () => {
    if (state.screen === 'game' && !state.over) {
      showToast('Connection lost. Refresh the page to rejoin.', false);
      els.disconnect.classList.remove('hidden');
    }
  };
  ws.onerror = () => {};
}

function send(obj) {
  if (state.ws && state.ws.readyState === WebSocket.OPEN) {
    state.ws.send(JSON.stringify(obj));
    return true;
  }
  showToast('Not connected — refresh to rejoin.', false);
  return false;
}

function handleServerMessage(msg) {
  switch (msg.type) {
    case 'hello':
      state.seat = msg.seat;
      state.balance = msg.balance;
      state.code = msg.code;
      els.sideBadge.textContent = msg.seat === 0 ? 'YOU · BLUE' : 'YOU · RED';
      els.sideBadge.className = 'side-badge ' + (msg.seat === 0 ? 'a' : 'b');
      buildShop();
      buildUpgradePanels();
      applyCouponFromHello(msg.coupon || null);
      break;
    case 'couponUpdate':
      applyCouponFromHello(msg.coupon);
      if (msg.exhaustedCode) { CouponStore.remove(msg.exhaustedCode); updateCouponRemoveButton(); }
      break;
    case 'couponWait':
      els.gameover.classList.add('hidden');
      els.waiting.classList.remove('hidden');
      $('coupon-wait-message').textContent = msg.message;
      $('btn-without-coupon').classList.remove('hidden');
      break;
    case 'matchStart':
      $('btn-without-coupon').classList.add('hidden');
      els.waiting.classList.add('hidden');
      els.shopToggle.classList.remove('hidden');
      SFX.setMusicHot(false);
      SFX.startMusic();
      if (isCompact()) els.shopToggle.classList.add('pulse');
      break;
    case 'snapshot':
      onSnapshot(msg);
      break;
    case 'matchReset':
      resetForRematch();
      SFX.setMusicHot(false);
      SFX.startMusic();
      break;
    case 'emote':
      showEmoteBubble(msg.seat, msg.emote);
      addFeedItem({ kind: 'emote', text: (msg.seat === state.seat ? 'You' : 'Foe') + ': ' + emoteGlyph(msg.emote) });
      break;
    case 'rematchStatus': {
      const mine = msg.seat === state.seat;
      els.rematchStatus.textContent = mine
        ? 'Rematch requested — waiting for opponent…'
        : 'Opponent wants a rematch!';
      break;
    }
    case 'opponentDisconnected':
      els.disconnect.classList.remove('hidden');
      break;
    case 'opponentConnected':
      els.disconnect.classList.add('hidden');
      break;
    case 'error':
      SFX.error();
      showToast(msg.message || 'Action rejected', false);
      if (/coupon/i.test(msg.message || '')) { applyCouponFromHello(null); clearSelection(); refreshCouponStatus(); }
      break;
  }
}

// ---------------------------------------------------------------------------
// Snapshots: HUD + FX ingestion
// ---------------------------------------------------------------------------
function onSnapshot(snap) {
  const prev = state.snapNow;
  state.snapPrev = prev;
  state.snapPrevAt = state.snapNowAt;
  state.snapNow = snap;
  state.snapNowAt = performance.now();

  ingestFx(prev, snap);
  updateClock(snap);
  updateEconomyHUD(snap);
  updateUpgradeButtons(snap);
  updateTurretButtons(snap);
  updateAbilityButtons(snap);
  updateBreachWarning(snap);
  for (const ev of snap.events || []) handleEvent(ev);

  if (snap.finished && !state.over) {
    state.over = true;
    showGameOver(snap);
  }
}

function handleEvent(ev) {
  switch (ev.kind) {
    case 'windfall':
      showBanner(`💰 Windfall! Both players +${ev.amount || 0}`, 'gold');
      SFX.windfall();
      els.coinChip && els.coinChip.classList.add('coin-pulse');
      setTimeout(() => els.coinChip && els.coinChip.classList.remove('coin-pulse'), 900);
      break;
    case 'firstBlood':
      SFX.firstBlood();
      break;
    case 'turretDestroyed':
      // Explosion already spawned from hp diff; just the horn-ish thud.
      break;
    case 'overtime':
      SFX.firstBlood(); // dramatic low horn signals the phase change
      SFX.setMusicHot(true);
      break;
    case 'supply':
      showBanner(`📦 Supplies dropped — Lane ${(ev.lane ?? 0) + 1}!`, 'blue');
      SFX.cacheDrop();
      break;
    case 'supplyClaim': {
      const lane = ev.lane ?? 0;
      goldBurst(px(50), laneY(lane));
      fx.floaters.push({
        x: px(50), y: laneY(lane) - 10, vy: -45, life: 1.1, maxLife: 1.1,
        text: '+' + (ev.amount || 0) + 'c', color: '#fde047',
      });
      SFX.windfall();
      break;
    }
    case 'supplyGone':
      break; // feed only
    case 'streak':
      showBanner('⚔ ' + ev.text, 'red');
      SFX.streakHorn();
      break;
    case 'bounty':
      showBanner(`💰 ${ev.text}`, 'gold');
      SFX.windfall();
      break;
    case 'warcry':
      showBanner(`📯 ${ev.text}`, 'blue');
      SFX.upgrade();
      break;
    case 'combatStart':
      showBanner(`⚔️ ${ev.text}`, 'red');
      SFX.firstBlood();
      break;
    case 'roundPrep':
      showBanner(`🛡️ Round ${state.snapNow?.round || ''} Prep`, 'blue');
      break;
    case 'ready':
      break;
    case 'breach':
      SFX.breach();
      fx.shake = Math.max(fx.shake, 9);
      break;
    case 'draw':
      SFX.breach();
      break;
    case 'resign':
      break;
  }
  addFeedItem(ev);
}

function updateClock(snap) {
  const t = Math.max(0, Math.ceil(snap.phaseRemaining || 0));
  const mm = Math.floor(t / 60);
  const ss = (t % 60).toString().padStart(2, '0');
  els.clock.textContent = `${mm}:${ss}`;
  els.clock.classList.toggle('overtime', !!snap.overtime);
  if (els.roundBadge) {
    els.roundBadge.textContent = `ROUND ${snap.round || 1}/${snap.maxRounds || 10}`;
  }
  const isPrep = snap.phase === 'prep';
  els.phase.textContent = isPrep ? 'PREP PHASE' : 'COMBAT';
  els.phase.className = 'phase-pill ' + (isPrep ? 'prep' : 'combat') + (snap.overtime ? ' overtime' : '');

  if (els.btnReady) {
    const myReady = !!(snap.ready && snap.ready[state.seat]);
    const foeReady = !!(snap.ready && snap.ready[otherSeat()]);
    els.btnReady.disabled = !isPrep || state.over;
    els.btnReady.classList.toggle('is-ready', myReady);
    els.btnReady.textContent = myReady ? '✓ READY' : 'READY';
    if (els.readyStatus) {
      els.readyStatus.textContent = isPrep
        ? (foeReady ? 'Foe: READY!' : 'Foe: Planning…')
        : 'Armies clashing!';
    }
  }
  if (snap.overtime) {
    els.turretPower.classList.remove('hidden');
    els.turretPower.textContent = `ESCALATION (R${snap.round}+): +Speed & +Tower Dmg`;
  } else {
    els.turretPower.classList.add('hidden');
  }
}

function otherSeat() { return state.seat === 0 ? 1 : 0; }

function updateEconomyHUD(snap) {
  const you = snap.players[state.seat];
  const foe = snap.players[otherSeat()];
  els.coinsYou.textContent = you.coins;
  els.coinsFoe.textContent = foe.coins;
  $('lives-you').textContent = `♥ ${you.lives}`;
  $('lives-foe').textContent = `♥ ${foe.lives}`;
  els.incomeYou.textContent = `+${you.income}/rnd${you.factory ? ' 🏭' : ''}`;
  els.incomeFoe.textContent = `+${foe.income}/rnd${foe.factory ? ' 🏭' : ''}`;
  updatePoolBar(you);
  refreshAffordability(you.coins, snap);
}

function updatePoolBar(you) {
  if (!els.poolBar) return;
  const pool = you.soldierPool || {};
  const entries = ALL_UNITS.filter((u) => (pool[u] || 0) > 0);
  if (entries.length === 0) {
    els.poolBar.classList.add('hidden');
    els.poolBar.innerHTML = '';
    return;
  }
  els.poolBar.classList.remove('hidden');
  const b = state.balance;
  els.poolBar.innerHTML = '<span style="color:var(--good);font-weight:700">Pool (FREE):</span>' +
    entries.map((u) => `<button class="pool-chip" data-pool-unit="${u}">${(b && b.units[u]?.name) || u} ×${pool[u]}</button>`).join('');
  els.poolBar.querySelectorAll('.pool-chip').forEach((chip) => {
    chip.addEventListener('click', () => { SFX.click(); selectUnit(chip.dataset.poolUnit); });
  });
}

// Red pulsing warning when an enemy closing in on *your* breach line.
function updateBreachWarning(snap) {
  if (snap.finished) { els.breachWarn.classList.add('hidden'); return; }
  let best = null;
  for (const u of snap.units || []) {
    if (u.side === state.seat) continue;
    const dist = state.seat === 0 ? u.x : 100 - u.x;
    if (best === null || dist < best.dist) best = { dist, lane: u.lane };
  }
  const DANGER = 18;
  if (best && best.dist < DANGER) {
    els.breachWarn.textContent = `⚠ BREACH IMMINENT — LANE ${best.lane + 1}`;
    els.breachWarn.classList.remove('hidden');
    const now = performance.now();
    if (now - fx.lastAlarmAt > 2500) {
      fx.lastAlarmAt = now;
      SFX.firstBlood();
    }
  } else {
    els.breachWarn.classList.add('hidden');
  }
}

function addFeedItem(ev) {
  const li = document.createElement('li');
  li.className = ev.kind || '';
  li.textContent = ev.text;
  els.feed.prepend(li);
  while (els.feed.children.length > 8) els.feed.removeChild(els.feed.lastChild);
}

function showGameOver(snap) {
  SFX.stopMusic();
  const iWon = !snap.draw && snap.winner === state.seat;
  // Reveal confetti canvas + launch if victorious
  if (iWon) launchConfetti();
  const title = els.resultTitle;
  if (snap.draw) {
    title.textContent = 'DRAW';
    title.className = 'draw';
    els.resultDetail.textContent = snap.time >= snap.timeLimit
      ? 'Time ran out — neither army broke through.'
      : 'Both armies breached on the same tick.';
    SFX.breach();
  } else if (iWon) {
    title.textContent = 'VICTORY';
    title.className = 'win';
    els.resultDetail.textContent = snap.winLane >= 0
      ? `Your forces breached the enemy line in lane ${snap.winLane + 1}!`
      : 'Your opponent resigned.';
    SFX.win();
  } else {
    title.textContent = 'DEFEAT';
    title.className = 'lose';
    els.resultDetail.textContent = snap.winLane >= 0
      ? `The enemy breached in lane ${snap.winLane + 1}.`
      : 'You resigned.';
    SFX.lose();
  }
  document.querySelectorAll('.shop-item, .upgrade-btn, .turret-cell button').forEach((b) => { b.disabled = true; });
  els.couponPanel.querySelectorAll('.ability-btn').forEach((b) => { b.disabled = true; });
  setSidebar(false);
  els.shopToggle.classList.add('hidden');
  // Match stats table
  const you = snap.players[state.seat], foe = snap.players[otherSeat()];
  const row = (label, a, b) =>
    `<div class="ms-row"><span>${label}</span><b class="ms-you">${a}</b><b class="ms-foe">${b}</b></div>`;
  els.matchStats.innerHTML =
    '<div class="ms-row ms-head"><span></span><span>YOU</span><span>FOE</span></div>' +
    row('Damage dealt', Math.round(you.damageDealt), Math.round(foe.damageDealt)) +
    row('Kills', you.kills, foe.kills) +
    row('Units lost', you.unitsLost, foe.unitsLost) +
    row('Turrets destroyed', you.turretsDown, foe.turretsDown);
  state.rematchPending = false;
  els.rematchBtn.disabled = false;
  els.rematchBtn.textContent = 'Rematch';
  els.rematchStatus.textContent = '';
  els.resignBtn.disabled = true;
  $('gameover-content').scrollTop = 0;
  els.gameover.classList.remove('hidden');
}

function resetForRematch() {
  state.over = false;
  state.rematchPending = false;
  state.snapNow = null;
  state.snapPrev = null;
  fx.projectiles.length = 0;
  fx.particles.length = 0;
  fx.floaters.length = 0;
  fx.hpCache.clear();
  fx.shake = 0;
  els.gameover.classList.add('hidden');
  els.waiting.classList.add('hidden');
  $('btn-without-coupon').classList.add('hidden');
  els.feed.innerHTML = '';
  els.shop.querySelectorAll('.shop-item').forEach((b) => { b.disabled = false; });
  els.turretUpgrades.querySelectorAll('button').forEach((b) => { b.disabled = false; });
  els.unitUpgrades.querySelectorAll('.upgrade-btn').forEach((b) => { b.disabled = false; });
  els.couponPanel.querySelectorAll('.ability-btn').forEach((b) => { b.disabled = false; });
  els.resignBtn.disabled = false;
  resetResignButton();
  els.shopToggle.classList.remove('hidden');
  els.rematchBtn.disabled = false;
  els.rematchBtn.textContent = 'Rematch';
  showToast('Rematch started!', true);
}

// ---------------------------------------------------------------------------
// Shop / upgrades + snapshot FX ingestion
// ---------------------------------------------------------------------------
function buildShop() {
  const b = state.balance;
  if (!b) return;
  els.shop.innerHTML = '';
  UNIT_TYPES.forEach((type, i) => {
    const u = b.units[type];
    if (!u) return;
    const btn = document.createElement('button');
    btn.className = 'shop-item';
    btn.dataset.unit = type;
    btn.dataset.cost = u.cost;
    btn.innerHTML =
      '<span class="si-key">' + (i + 1) + '</span>' +
      '<div class="si-top"><span class="si-name">' + u.name + '</span><span class="si-cost">' + u.cost + 'c</span></div>' +
      '<div class="si-role">' + u.role + '</div>';
    btn.addEventListener('click', () => { SFX.click(); selectUnit(type); });
    els.shop.appendChild(btn);
  });
  addCouponShopItems();
  const turretBtn = document.createElement('button');
  turretBtn.className = 'shop-item';
  turretBtn.dataset.cost = b.turret.cost;
  turretBtn.dataset.kind = 'turret';
  turretBtn.innerHTML =
    '<span class="si-key">T</span>' +
    '<div class="si-top"><span class="si-name">' + b.turret.name + '</span><span class="si-cost">' + b.turret.cost + 'c</span></div>' +
    '<div class="si-role">Two slots per lane. Blocks &amp; fires on ground troops.</div>';
  turretBtn.addEventListener('click', () => { SFX.click(); selectTurret(); });
  els.shop.appendChild(turretBtn);
  if (b.wall) {
    const wallBtn = document.createElement('button');
    wallBtn.className = 'shop-item';
    wallBtn.dataset.cost = b.wall.cost;
    wallBtn.dataset.kind = 'wall';
    wallBtn.innerHTML =
      '<span class="si-key">W</span>' +
      '<div class="si-top"><span class="si-name">🧱 ' + b.wall.name + '</span><span class="si-cost">' + b.wall.cost + 'c</span></div>' +
      '<div class="si-role">High HP &amp; Shield barrier. Stalls raiders; upgradeable &amp; rebuildable.</div>';
    wallBtn.addEventListener('click', () => { SFX.click(); selectWall(); });
    els.shop.appendChild(wallBtn);
  }
  if (b.turn && b.turn.scoutCost) {
    const scoutBtn = document.createElement('button');
    scoutBtn.className = 'shop-item';
    scoutBtn.dataset.cost = b.turn.scoutCost;
    scoutBtn.dataset.kind = 'scout';
    scoutBtn.innerHTML =
      '<span class="si-key">S</span>' +
      '<div class="si-top"><span class="si-name">🔭 Scout Flare</span><span class="si-cost">' + b.turn.scoutCost + 'c</span></div>' +
      '<div class="si-role">Reveals hidden luck items &amp; enemy staging in 1 lane during Prep.</div>';
    scoutBtn.addEventListener('click', () => { SFX.click(); selectScout(); });
    els.shop.appendChild(scoutBtn);
  }
  const fac = b.economy;
  const facBtn = document.createElement('button');
  facBtn.className = 'shop-item factory-item';
  facBtn.dataset.cost = fac.factoryCost;
  facBtn.dataset.factory = '1';
  facBtn.innerHTML =
    '<span class="si-key">F</span>' +
    '<div class="si-top"><span class="si-name">Coin Factory</span><span class="si-cost">' + fac.factoryCost + 'c</span></div>' +
    '<div class="si-role">+' + fac.factoryIncome + ' coins every round. One per player (upgradeable).</div>';
  facBtn.addEventListener('click', () => { SFX.click(); send({ type: state.snapNow?.players[state.seat].factory ? 'upgradeFactory' : 'factory' }); });
  els.shop.appendChild(facBtn);
}

function buildUpgradePanels() {
  const b = state.balance;
  if (!b) return;
  if (els.upgradeUnitTabs) {
    els.upgradeUnitTabs.innerHTML = '';
    ALL_UNITS.forEach((type) => {
      const u = b.units[type];
      if (!u) return;
      const tab = document.createElement('button');
      tab.className = 'up-tab' + (state.selectedUpgradeUnit === type ? ' active' : '') + (u.couponOnly && !state.coupon ? ' locked' : '');
      tab.dataset.unit = type;
      tab.textContent = (u.couponOnly && !state.coupon ? '🔒 ' : '') + u.name;
      tab.addEventListener('click', () => {
        SFX.click();
        state.selectedUpgradeUnit = type;
        buildUpgradePanels();
        if (state.snapNow) updateUpgradeButtons(state.snapNow);
      });
      els.upgradeUnitTabs.appendChild(tab);
    });
  }

  els.unitUpgrades.innerHTML = '';
  const type = state.selectedUpgradeUnit || 'soldier';
  const u = b.units[type];
  if (u) {
    TRACKS.forEach((tr) => {
      const row = document.createElement('div');
      const isSpecialLocked = tr.couponOnly && !state.coupon;
      const isUnitLocked = u.couponOnly && !state.coupon;
      row.className = 'upgrade-row' + ((isSpecialLocked || isUnitLocked) ? ' locked-track' : '');
      row.dataset.unit = type;
      row.dataset.track = tr.id;
      const maxPips = tr.id === 'special' ? 1 : 2;
      let pipsHtml = '<span class="pips">';
      for (let i = 0; i < maxPips; i++) pipsHtml += `<span class="pip" data-pip="${i}"></span>`;
      pipsHtml += '</span>';
      const label = tr.id === 'special' ? `${tr.label}: ${u.specialName || 'Elite'}` : tr.label;
      row.innerHTML =
        `<span class="u-name" title="${tr.id === 'special' ? (u.specialDesc || '') : ''}">${label}</span>` +
        pipsHtml +
        `<button class="upgrade-btn">Upgrade</button>`;
      row.querySelector('.upgrade-btn').addEventListener('click', () => {
        SFX.click();
        send({ type: 'upgradeUnit', unit: type, track: tr.id });
      });
      els.unitUpgrades.appendChild(row);
    });
  }

  els.turretUpgrades.innerHTML = '';
  const slotCount = (b.turret && b.turret.slots) || 2;
  for (let lane = 0; lane < 5; lane++) {
    const cell = document.createElement('div');
    cell.className = 'turret-cell';
    cell.dataset.lane = String(lane);
    let html = '<div class="tc-lane">LANE ' + (lane + 1) + '</div>';
    for (let slot = 0; slot < slotCount; slot++) {
      html +=
        '<div class="tc-slot" data-slot="' + slot + '">' +
          '<div class="tc-slot-head"><span class="tc-tag">' + String.fromCharCode(65 + slot) + '</span>' +
          '<span class="tc-level">—</span></div>' +
          '<button class="tc-up-btn" data-track="damage">⚔️ Dmg</button>' +
          '<button class="tc-up-btn" data-track="shield">🛡️ Shd</button>' +
          '<button class="tc-up-btn" data-track="special">' + (state.coupon ? '✈️ Flak' : '🔒 Flak') + '</button>' +
        '</div>';
    }
    html +=
      '<div class="tc-slot tc-wall">' +
        '<div class="tc-slot-head"><span class="tc-tag">W</span><span class="tc-wall-level">—</span></div>' +
        '<button class="tc-wall-btn">🧱 Build</button>' +
      '</div>';
    cell.innerHTML = html;
    cell.querySelectorAll('.tc-slot[data-slot] button').forEach((btn) => {
      btn.addEventListener('click', () => {
        SFX.click();
        send({ type: 'upgradeTurret', lane, slot: Number(btn.closest('.tc-slot').dataset.slot), track: btn.dataset.track });
      });
    });
    const wallBtn = cell.querySelector('.tc-wall-btn');
    if (wallBtn) {
      wallBtn.addEventListener('click', () => {
        SFX.click();
        const hasWall = (state.snapNow?.walls || []).some((w) => w.side === state.seat && w.lane === lane);
        send({ type: hasWall ? 'upgradeWall' : 'wall', lane });
      });
    }
    els.turretUpgrades.appendChild(cell);
  }
}

function refreshAffordability(coins, snap) {
  const isPrep = !snap || snap.phase === 'prep';
  const you = snap && snap.players && snap.players[state.seat];
  const pool = (you && you.soldierPool) || {};
  els.shop.querySelectorAll('.shop-item').forEach((btn) => {
    const unit = btn.dataset.unit;
    const isCouponUnit = unit === 'dragon' || unit === 'valkyrie';
    if (state.over || !isPrep) {
      btn.disabled = true;
      btn.title = !isPrep ? 'Available in Preparation Phase' : '';
      return;
    }
    if (isCouponUnit && !state.coupon) {
      btn.disabled = true;
      btn.title = '🔒 Requires an active coupon';
      return;
    }
    btn.title = '';
    const inPool = unit ? (pool[unit] || 0) : 0;
    const cost = Number(btn.dataset.cost || '0');
    const costEl = btn.querySelector('.si-cost');
    if (costEl && unit) {
      costEl.innerHTML = inPool > 0
        ? `<span style="color:var(--good)">FREE</span> <span class="si-pool-badge">×${inPool}</span>`
        : `${cost}c`;
    }
    const canAfford = inPool > 0 || coins >= cost;
    btn.classList.toggle('cant-afford', !canAfford);
    btn.disabled = !canAfford;
  });
}

function updateUpgradeButtons(snap) {
  const you = snap.players[state.seat];
  const b = state.balance;
  if (!b) return;
  const isPrep = snap.phase === 'prep';
  const trackMap = (you.unitTrackUpgrades && you.unitTrackUpgrades[state.selectedUpgradeUnit]) || {};
  const u = b.units[state.selectedUpgradeUnit];
  if (!u) return;

  els.unitUpgrades.querySelectorAll('.upgrade-row').forEach((row) => {
    const track = row.dataset.track;
    const level = trackMap[track] || 0;
    const maxLvl = track === 'special' ? 1 : 2;
    row.querySelectorAll('.pip').forEach((pip, i) => pip.classList.toggle('on', i < level));
    const btn = row.querySelector('.upgrade-btn');
    if ((u.couponOnly || track === 'special') && !state.coupon) {
      btn.textContent = '🔒 Coupon';
      btn.disabled = true;
      return;
    }
    if (level >= maxLvl) {
      btn.textContent = 'MAX';
      btn.disabled = true;
    } else {
      const costs = (u.trackCosts && u.trackCosts[track]) || u.upgradeCosts;
      const cost = costs[level] || u.upgradeCosts[level];
      btn.textContent = `Lv${level + 1} · ${cost}c`;
      btn.disabled = !isPrep || you.coins < cost || state.over;
    }
  });
}

function updateTurretButtons(snap) {
  const b = state.balance;
  if (!b) return;
  const isPrep = snap.phase === 'prep';
  const you = snap.players[state.seat];
  const turrets = snap.turrets || [];
  const myTurrets = {};
  turrets.forEach((t) => { if (t.side === state.seat) myTurrets[t.lane + ':' + (t.slot || 0)] = t; });
  const myWalls = {};
  (snap.walls || []).forEach((w) => { if (w.side === state.seat) myWalls[w.lane] = w; });
  els.turretUpgrades.querySelectorAll('.turret-cell').forEach((cell) => {
    const lane = Number(cell.dataset.lane);
    cell.querySelectorAll('.tc-slot[data-slot]').forEach((slotEl) => {
      const slot = Number(slotEl.dataset.slot);
      const t = myTurrets[lane + ':' + slot];
      const lvlEl = slotEl.querySelector('.tc-level');
      const btns = slotEl.querySelectorAll('button');
      if (state.over || !t) {
        slotEl.classList.toggle('empty', !t);
        lvlEl.textContent = t ? '●'.repeat(t.level) : '—';
        btns.forEach((btn) => { btn.disabled = true; });
        return;
      }
      slotEl.classList.remove('empty');
      const tl = t.trackLevels || {};
      lvlEl.textContent = `⚔${tl.damage || 0} 🛡${tl.shield || 0}${tl.special ? ' ✈' : ''}`;
      btns.forEach((btn) => {
        const track = btn.dataset.track;
        if (track === 'special' && !state.coupon) {
          btn.textContent = '🔒 Flak';
          btn.disabled = true;
          return;
        }
        const cur = tl[track] || 0;
        const maxLvl = track === 'special' ? 1 : 2;
        if (cur >= maxLvl) {
          btn.textContent = 'MAX';
          btn.disabled = true;
        } else {
          const costs = (b.turret.trackCosts && b.turret.trackCosts[track]) || b.turret.upgradeCosts;
          const cost = costs[cur] || b.turret.upgradeCosts[cur];
          btn.textContent = (track === 'damage' ? '⚔ ' : track === 'shield' ? '🛡 ' : '✈ ') + cost + 'c';
          btn.disabled = !isPrep || you.coins < cost || snap.overtime;
        }
      });
    });
    const wallEl = cell.querySelector('.tc-wall');
    if (wallEl && b.wall) {
      const w = myWalls[lane];
      const lvlEl = wallEl.querySelector('.tc-wall-level');
      const btn = wallEl.querySelector('.tc-wall-btn');
      if (!w) {
        wallEl.classList.add('empty');
        lvlEl.textContent = '—';
        btn.textContent = `🧱 ${b.wall.cost}c`;
        btn.disabled = state.over || !isPrep || you.coins < b.wall.cost || snap.overtime;
      } else if (w.level >= 2) {
        wallEl.classList.remove('empty');
        lvlEl.textContent = 'Lv3';
        btn.textContent = 'MAX';
        btn.disabled = true;
      } else {
        wallEl.classList.remove('empty');
        lvlEl.textContent = `Lv${w.level + 1}`;
        const cost = b.wall.upgradeCosts[w.level];
        btn.textContent = `🧱 Lv${w.level + 2} · ${cost}c`;
        btn.disabled = state.over || !isPrep || you.coins < cost || snap.overtime;
      }
    }
  });
  els.shop.querySelectorAll('[data-factory]').forEach((btn) => {
    const level = you.factoryLevel || 0;
    const cost = you.factory ? [75, 125][level] : b.economy.factoryCost;
    btn.disabled = !isPrep || state.over || (you.factory && level >= 2) || you.coins < cost;
    btn.querySelector('.si-cost').textContent = you.factory && level >= 2 ? 'MAX' : cost + 'c';
    if (you.factory) btn.querySelector('.si-role').textContent = `+${b.economy.factoryIncome * (level + 1)} coins/round${level < 2 ? ' · Upgrade' : ' · MAX'}`;
  });
}

function selectUnit(type) {
  // Touch: tapping the selected item again cancels (no right-click available).
  // Mouse: clicking it again just keeps it selected (spam-deploy friendly).
  if (coarsePointer && state.selection && state.selection.kind === 'unit' && state.selection.unit === type) { clearSelection(); return; }
  state.selection = { kind: 'unit', unit: type };
  highlightSelection();
  if (isCompact()) setSidebar(false); // reveal the battlefield for tap-to-deploy
}
function selectTurret() {
  if (coarsePointer && state.selection && state.selection.kind === 'turret') { clearSelection(); return; }
  state.selection = { kind: 'turret' };
  highlightSelection();
  if (isCompact()) setSidebar(false);
}
function selectWall() {
  if (coarsePointer && state.selection && state.selection.kind === 'wall') { clearSelection(); return; }
  state.selection = { kind: 'wall' };
  highlightSelection();
  if (isCompact()) setSidebar(false);
}
function selectScout() {
  if (coarsePointer && state.selection && state.selection.kind === 'scout') { clearSelection(); return; }
  state.selection = { kind: 'scout' };
  highlightSelection();
  if (isCompact()) setSidebar(false);
}
function clearSelection() { state.selection = null; highlightSelection(); }
function highlightSelection() {
  const sel = state.selection;
  els.shop.querySelectorAll('.shop-item').forEach((btn) => {
    const isUnit = sel && sel.kind === 'unit' && btn.dataset.unit === sel.unit;
    const isTurret = sel && sel.kind === 'turret' && btn.dataset.kind === 'turret';
    const isWall = sel && sel.kind === 'wall' && btn.dataset.kind === 'wall';
    const isScout = sel && sel.kind === 'scout' && btn.dataset.kind === 'scout';
    btn.classList.toggle('selected', !!(isUnit || isTurret || isWall || isScout));
  });
  const isAbility = sel && sel.kind === 'ability';
  ABILITY_EFFECTS.forEach((effect) => {
    const btn = abilityButtonFor(effect);
    if (btn) btn.classList.toggle('selected', !!(isAbility && sel.effect === effect));
  });
  const abilityNames = { rockslide: 'Rockslide', dragonfire: 'Dragon Fire', warcry: 'Warcry Blessing' };
  els.selHint.textContent = sel
    ? (sel.kind === 'turret'
        ? (coarsePointer ? 'Tap a lane to build in a free slot. Tap the turret again to cancel.' : 'Click a lane to build in a free turret slot. Right-click / Esc to cancel.')
        : sel.kind === 'wall'
          ? 'Click a lane to build, upgrade, or rebuild a Reinforcement Wall. Right-click / Esc to cancel.'
          : sel.kind === 'scout'
            ? 'Click a lane to launch a Scout Flare (reveals hidden luck & staged enemies).'
            : sel.kind === 'ability'
              ? (coarsePointer ? `Tap a lane to cast ${abilityNames[sel.effect] || 'the effect'}. Tap the card again to cancel.` : `Click a lane to unleash ${abilityNames[sel.effect] || 'the effect'}. Right-click / Esc to cancel.`)
              : (coarsePointer ? 'Tap a lane to deploy. Tap the item again to cancel.' : 'Click a lane to deploy. Right-click / Esc to cancel.'))
    : (coarsePointer ? 'Tap a unit, then tap a lane.' : 'Select a unit, then click a lane. Right-click / Esc to cancel.');
}

// ---- FX ingestion (called from onSnapshot) ----

function entityKey(kind, ref) { return kind + ':' + ref; }

function ingestFx(prev, snap) {
  const seen = new Set();
  const now = performance.now();

  const track = (key, fxX, lane, yOff, hp, side, kind) => {
    seen.add(key);
    const before = fx.hpCache.get(key);
    if (before) {
      const lost = before.hp - hp;
      if (lost > 0.5) {
        const dispY = laneY(lane) + (yOff || 0) * LANE_H - 16;
        fx.floaters.push({
          x: px(fxX), y: dispY, vy: -52, life: 0.85, maxLife: 0.85,
          text: '-' + Math.round(lost),
          color: side === state.seat ? '#fda4af' : '#fde68a',
        });
        fx.flashes.push({ x: px(fxX), y: laneY(lane) + (yOff || 0) * LANE_H, r: 16, until: now + 110, color: 'rgba(255,255,255,0.55)' });
        SFX.hit();
      }
    }
    fx.hpCache.set(key, { hp, x: fxX, lane, yOff: yOff || 0, side, kind });
  };

  for (const u of snap.units || []) track(entityKey('u', u.id), u.x, u.lane, u.yOff, u.hp, u.side, 'unit');
  for (const t of snap.turrets || []) track(entityKey('t', t.side + ':' + t.lane + ':' + (t.slot || 0)), turretX(t.side), t.lane, TURRET_SLOT_OFF[t.slot] || 0, t.hp, t.side, 'turret');

  // Entities present before but gone now → death poof / turret explosion.
  for (const [key, info] of [...fx.hpCache]) {
    if (seen.has(key)) continue;
    fx.hpCache.delete(key);
    const dy = laneY(info.lane) + info.yOff * LANE_H;
    if (info.kind === 'turret') {
      boomAt(px(info.x), laneY(info.lane) + info.yOff * LANE_H, 1.4);
      fx.shake = Math.max(fx.shake, 7);
      SFX.boom();
    } else {
      poofAt(px(info.x), dy, 7);
      SFX.poof();
    }
  }

  // Server-authored shots / melee → projectiles, muzzle flashes, sparks.
  for (const f of snap.fx || []) {
    if (f.kind === 'shot') {
      spawnProjectile(f);
      const isSiege = f.unit === 'siege';
      const isTurret = f.unit === 'turret';
      const isDragon = f.unit === 'dragon';
      const fy = (isTurret ? turretSlotY(f.lane, f.slot || 0) : laneY(f.lane)) - (isDragon ? 14 : 4);
      fx.flashes.push({
        x: px(f.fromX), y: fy,
        r: isSiege ? 16 : isDragon ? 14 : isTurret ? 9 : 7,
        until: now + (isSiege ? 150 : isDragon ? 130 : 90),
        color: isDragon ? 'rgba(251,146,60,0.85)' : isSiege ? 'rgba(253,224,71,0.9)' : 'rgba(254,240,138,0.85)',
      });
      if (isSiege) SFX.shell(); else if (isTurret) SFX.turretShot(); else if (isDragon) SFX.fire(); else SFX.arrow();
    } else if (f.kind === 'melee') {
      meleeSparks(f.toX, f.lane, 4);
    } else if (f.kind === 'rockslide') {
      rockslideFx(f.lane);
    } else if (f.kind === 'dragonfire') {
      dragonFireFx(f.lane);
    } else if (f.kind === 'burn') {
      emberBurst(f.lane, 4);
    } else if (f.kind === 'bounty' && f.side === state.seat) {
      fx.floaters.push({
        x: px(f.fromX), y: laneY(f.lane) - 22, vy: -48, life: 1.0, maxLife: 1.0,
        text: `+${f.unit}c`, color: '#fde047',
      });
    } else if (f.kind === 'warcry') {
      goldBurst(px(f.side === 0 ? 18 : 82), laneY(f.lane));
    }
  }
}

// ---------------------------------------------------------------------------
// FX: projectiles, particles, floaters, flashes, shake
// All FX positions are PIXEL coordinates on the canvas (unless noted).
// ---------------------------------------------------------------------------
function turretX(side) {
  return side === 0 ? 10 : 90;
}
const px = (fieldX) => (fieldX / 100) * (FIELD.x1 - FIELD.x0) + FIELD.x0;

function spawnProjectile(f) {
  const isDragon = f.unit === 'dragon';
  const y = (f.unit === 'turret' ? turretSlotY(f.lane, f.slot || 0) : laneY(f.lane)) - (isDragon ? 14 : 4);
  const x0 = px(f.fromX), x1 = px(f.toX);
  const dist = Math.abs(x1 - x0);
  let kind = 'arrow', dur = Math.min(0.5, Math.max(0.16, dist / 620)), arc = 14;
  if (f.unit === 'siege') { kind = 'shell'; dur = Math.min(1.15, Math.max(0.45, dist / 430)); arc = 78; }
  else if (f.unit === 'turret') { kind = 'bullet'; dur = Math.max(0.07, dist / 980); arc = 0; }
  else if (isDragon) { kind = 'fireball'; dur = Math.min(0.45, Math.max(0.18, dist / 700)); arc = 6; }
  fx.projectiles.push({
    kind, x0, x1, y0: y, y1: y, y, t: 0, dur, arc,
    dir: Math.sign(x1 - x0) || 1,
  });
}

function meleeSparks(fieldXPos, lane, n) {
  const x = px(fieldXPos), y = laneY(lane);
  n = n || 4;
  for (let i = 0; i < n; i++) {
    const a = Math.random() * Math.PI * 2;
    const sp = 40 + Math.random() * 60;
    fx.particles.push({
      x, y: y + (Math.random() - 0.5) * 10,
      vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 30,
      grav: 320, life: 0.32, maxLife: 0.32,
      size: 1.4 + Math.random() * 1.5, color: '#fde68a',
    });
  }
}

function poofAt(x, y, n) {
  for (let i = 0; i < (n || 7); i++) {
    const a = Math.random() * Math.PI * 2;
    const sp = 16 + Math.random() * 46;
    fx.particles.push({
      x, y,
      vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 24,
      grav: 55, life: 0.55, maxLife: 0.55,
      size: 2 + Math.random() * 3, color: 'rgba(203,213,225,0.85)',
    });
  }
}

function boomAt(x, y, scale) {
  scale = scale || 1;
  for (let i = 0; i < Math.floor(16 * scale); i++) {
    const a = Math.random() * Math.PI * 2;
    const sp = (45 + Math.random() * 130) * scale;
    fx.particles.push({
      x, y,
      vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 40,
      grav: 230, life: 0.6 + Math.random() * 0.3, maxLife: 0.9,
      size: (2 + Math.random() * 3.4) * scale,
      color: ['#fde68a', '#fb923c', '#f87171', '#e2e8f0'][Math.floor(Math.random() * 4)],
    });
  }
  fx.rings.push({ x, y, r: 6, maxR: 46 * scale, life: 0.45, maxLife: 0.45 });
}

// Rockfall telegraphed across a whole lane: rocks rain from the top edge,
// then the lane flashes. Damage itself is applied server-side.
function rockslideFx(lane) {
  const y = laneY(lane);
  for (let i = 0; i < 26; i++) {
    fx.particles.push({
      x: FIELD.x0 + Math.random() * (FIELD.x1 - FIELD.x0),
      y: -20 - Math.random() * 140,
      vx: (Math.random() - 0.5) * 70,
      vy: 240 + Math.random() * 180,
      grav: 950,
      life: 0.8 + Math.random() * 0.35,
      maxLife: 1.1,
      size: 2.5 + Math.random() * 4,
      color: ['#a8a29e', '#78716c', '#57534e', '#d6d3d1'][Math.floor(Math.random() * 4)],
    });
  }
  for (let i = 0; i < 5; i++) {
    fx.flashes.push({ x: FIELD.x0 + ((i + 0.5) / 5) * (FIELD.x1 - FIELD.x0), y, r: 90, until: performance.now() + 140, color: 'rgba(231,229,228,0.4)' });
  }
  fx.shake = Math.max(fx.shake, 9);
  SFX.rockfall();
}

// Ignition burst for Dragon Fire: a wall of flame along the lane.
function dragonFireFx(lane) {
  const y = laneY(lane);
  for (let i = 0; i < 34; i++) {
    fx.particles.push({
      x: FIELD.x0 + Math.random() * (FIELD.x1 - FIELD.x0),
      y: y + (Math.random() - 0.5) * LANE_H * 0.7,
      vx: (Math.random() - 0.5) * 90,
      vy: -40 - Math.random() * 90,
      grav: -60,
      life: 0.55 + Math.random() * 0.5,
      maxLife: 1.0,
      size: 2.5 + Math.random() * 3.5,
      color: ['#fdba74', '#fb923c', '#f97316', '#fde047', '#ef4444'][Math.floor(Math.random() * 5)],
    });
  }
  for (let i = 0; i < 5; i++) {
    fx.flashes.push({ x: FIELD.x0 + ((i + 0.5) / 5) * (FIELD.x1 - FIELD.x0), y, r: 110, until: performance.now() + 140, color: 'rgba(249,115,22,0.45)' });
  }
  fx.shake = Math.max(fx.shake, 5);
  SFX.inferno();
}

// A few embers for each burn tick so active fire zones shimmer.
function emberBurst(lane, n) {
  const y = laneY(lane);
  for (let i = 0; i < (n || 3); i++) {
    fx.particles.push({
      x: FIELD.x0 + Math.random() * (FIELD.x1 - FIELD.x0),
      y: y + (Math.random() - 0.5) * LANE_H * 0.75,
      vx: (Math.random() - 0.5) * 40,
      vy: -30 - Math.random() * 60,
      grav: -40,
      life: 0.5 + Math.random() * 0.4,
      maxLife: 0.9,
      size: 1.6 + Math.random() * 2.2,
      color: ['#fdba74', '#fb923c', '#fde047'][Math.floor(Math.random() * 3)],
    });
  }
}

function stepFx(dt) {
  for (let i = fx.projectiles.length - 1; i >= 0; i--) {
    const p = fx.projectiles[i];
    p.t += dt / p.dur;
    if (p.kind === 'shell' && Math.random() < 0.55) {
      const pos = projPos(p, Math.min(1, p.t));
      fx.particles.push({
        x: pos.x, y: pos.y - 2,
        vx: (Math.random() - 0.5) * 24, vy: -8 - Math.random() * 12,
        grav: 0, life: 0.38, maxLife: 0.38, size: 1.5 + Math.random(),
        color: 'rgba(148,163,184,0.65)',
      });
    } else if (p.kind === 'fireball' && Math.random() < 0.85) {
      const pos = projPos(p, Math.min(1, p.t));
      fx.particles.push({
        x: pos.x + (Math.random() - 0.5) * 4, y: pos.y + (Math.random() - 0.5) * 4,
        vx: (Math.random() - 0.5) * 30 - p.dir * 30, vy: (Math.random() - 0.5) * 30,
        grav: 0, life: 0.4, maxLife: 0.4, size: 1.6 + Math.random() * 1.8,
        color: ['#fdba74', '#fb923c', '#fde047'][Math.floor(Math.random() * 3)],
      });
    }
    if (p.t >= 1) {
      if (p.kind === 'shell') {
        boomAt(p.x1, p.y, 1.15);
        fx.shake = Math.max(fx.shake, 5);
        SFX.boom();
      } else if (p.kind === 'fireball') {
        for (let k = 0; k < 8; k++) {
          const a = Math.random() * Math.PI * 2;
          const sp = 40 + Math.random() * 70;
          fx.particles.push({
            x: p.x1, y: p.y, vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 30,
            grav: 160, life: 0.35 + Math.random() * 0.25, maxLife: 0.6,
            size: 1.6 + Math.random() * 2, color: ['#fb923c', '#fdba74', '#fde047'][k % 3],
          });
        }
        fx.flashes.push({ x: p.x1, y: p.y, r: 26, until: performance.now() + 120, color: 'rgba(251,146,60,0.7)' });
      } else {
        // small impact puff for arrows/bullets
        for (let k = 0; k < 3; k++) {
          const a = Math.random() * Math.PI * 2;
          fx.particles.push({
            x: p.x1, y: p.y, vx: Math.cos(a) * 45, vy: Math.sin(a) * 45 - 15,
            grav: 260, life: 0.22, maxLife: 0.22, size: 1.3, color: '#fef9c3',
          });
        }
      }
      fx.projectiles.splice(i, 1);
    }
  }
  for (let i = fx.particles.length - 1; i >= 0; i--) {
    const p = fx.particles[i];
    p.life -= dt;
    if (p.life <= 0) { fx.particles.splice(i, 1); continue; }
    p.vy += (p.grav || 0) * dt;
    p.x += p.vx * dt;
    p.y += p.vy * dt;
  }
  for (let i = fx.rings.length - 1; i >= 0; i--) {
    const r = fx.rings[i];
    r.life -= dt;
    r.r += (r.maxR * 2.2) * dt;
    if (r.life <= 0) fx.rings.splice(i, 1);
  }
  for (let i = fx.floaters.length - 1; i >= 0; i--) {
    const f = fx.floaters[i];
    f.life -= dt;
    f.y += f.vy * dt;
    f.vy *= 0.94;
    if (f.life <= 0) fx.floaters.splice(i, 1);
  }
  const now = performance.now();
  fx.flashes = fx.flashes.filter((f) => f.until > now);
  fx.shake *= Math.pow(0.0015, dt);
  if (fx.shake < 0.25) fx.shake = 0;
}

function projPos(p, t) {
  const tt = Math.min(Math.max(t, 0), 1);
  return {
    x: p.x0 + (p.x1 - p.x0) * tt,
    y: p.y - Math.sin(Math.PI * tt) * p.arc,
  };
}

// ---------------------------------------------------------------------------
// Rendering
// ---------------------------------------------------------------------------
function unitRenderX(u) {
  const prev = state.snapPrev && state.snapPrev.units.find((p) => p.id === u.id);
  if (!prev) return u.x;
  const t0 = state.snapPrevAt, t1 = state.snapNowAt;
  const span = Math.max(1, t1 - t0);
  const gamma = Math.min(1.25, Math.max(0, (performance.now() - t0) / span));
  return prev.x + (u.x - prev.x) * gamma;
}

const P = () => performance.now();
let lastFrame = 0;

function goldBurst(x, y) {
  for (let i = 0; i < 16; i++) {
    const a = Math.random() * Math.PI * 2;
    const sp = 40 + Math.random() * 95;
    fx.particles.push({
      x, y,
      vx: Math.cos(a) * sp, vy: Math.sin(a) * sp - 60,
      grav: 260, life: 0.55 + Math.random() * 0.2, maxLife: 0.75,
      size: 1.8 + Math.random() * 2.2,
      color: ['#fde047', '#fbbf24', '#fff7cc'][i % 3],
    });
  }
  fx.rings.push({ x, y, r: 4, maxR: 48, life: 0.5, maxLife: 0.5 });
}

function drawCaches(snap, now) {
  for (const c of snap.caches || []) {
    const x = px(c.x);
    const y = laneY(c.lane);
    const pulse = 0.75 + 0.25 * Math.sin(now / 300);
    // halo
    const g = ctx.createRadialGradient(x, y, 2, x, y, 26);
    g.addColorStop(0, 'rgba(253, 224, 71, ' + 0.5 * pulse + ')');
    g.addColorStop(1, 'rgba(253, 224, 71, 0)');
    ctx.fillStyle = g;
    ctx.beginPath(); ctx.arc(x, y, 26, 0, Math.PI * 2); ctx.fill();
    // crate body
    ctx.save();
    ctx.translate(x, y + Math.sin(now / 420) * 2); // gentle bob
    ctx.fillStyle = '#b45309';
    roundRect(ctx, -11, -8, 22, 16, 3); ctx.fill();
    ctx.fillStyle = '#f59e0b';
    roundRect(ctx, -11, -11, 22, 8, 3); ctx.fill();
    ctx.strokeStyle = '#78350f'; ctx.lineWidth = 1.5;
    roundRect(ctx, -11, -11, 22, 19, 3); ctx.stroke();
    ctx.fillStyle = '#fff7cc';
    ctx.font = '900 12px "Segoe UI", sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText('$', 0, 4.5);
    ctx.restore();
    // expiry ring
    const frac = Math.max(0, Math.min(1, (c.expiresIn || 0) / 25));
    ctx.strokeStyle = frac > 0.3 ? 'rgba(254, 240, 138, 0.85)' : 'rgba(248,113,113,0.9)';
    ctx.lineWidth = 2.5;
    ctx.beginPath();
    ctx.arc(x, y, 15, -Math.PI / 2, -Math.PI / 2 + Math.PI * 2 * frac);
    ctx.stroke();
  }
}

// Burning lanes (Dragon Fire). Drawn as a flickering amber wash with
// licking flame tongues; ember particles are spawned into the fx layer.
function drawBurns(snap, now) {
  const burns = snap.burns || [];
  for (const b of burns) {
    const y0 = FIELD.y0 + b.lane * LANE_H;
    const life = Math.max(0, Math.min(1, (b.remaining || 0) / 1.2)); // fade out at the end
    const flick = 0.75 + 0.25 * Math.sin(now / 90 + b.lane * 2.1) + 0.1 * Math.sin(now / 37 + b.lane);
    const a = 0.34 * life * flick;
    const g = ctx.createLinearGradient(0, y0, 0, y0 + LANE_H);
    g.addColorStop(0, 'rgba(249,115,22,0)');
    g.addColorStop(0.5, `rgba(249,115,22,${a})`);
    g.addColorStop(1, 'rgba(239,68,68,0)');
    ctx.fillStyle = g;
    ctx.fillRect(FIELD.x0, y0, FIELD.x1 - FIELD.x0, LANE_H);
    // flame tongues
    ctx.fillStyle = `rgba(253,186,116,${0.5 * life * flick})`;
    const tongues = 16;
    for (let i = 0; i < tongues; i++) {
      const tx = FIELD.x0 + ((i + 0.5) / tongues) * (FIELD.x1 - FIELD.x0);
      const h = (10 + 9 * Math.sin(now / 110 + i * 1.7 + b.lane)) * life;
      if (h <= 2) continue;
      ctx.beginPath();
      ctx.moveTo(tx - 6, y0 + LANE_H);
      ctx.quadraticCurveTo(tx, y0 + LANE_H - h * 2.2, tx + 6, y0 + LANE_H);
      ctx.closePath();
      ctx.fill();
    }
    // rising embers
    if (Math.random() < 0.5) {
      fx.particles.push({
        x: FIELD.x0 + Math.random() * (FIELD.x1 - FIELD.x0),
        y: y0 + LANE_H - 4,
        vx: (Math.random() - 0.5) * 24,
        vy: -35 - Math.random() * 55,
        grav: -30,
        life: 0.6 + Math.random() * 0.4,
        maxLife: 1.0,
        size: 1.4 + Math.random() * 1.6,
        color: ['#fdba74', '#fb923c', '#fde047'][Math.floor(Math.random() * 3)],
      });
    }
  }
}

function draw() {
  requestAnimationFrame(draw);
  const now = performance.now();
  const dt = lastFrame ? Math.min(0.05, (now - lastFrame) / 1000) : 0.016;
  lastFrame = now;
  stepFx(dt);

  ctx.clearRect(0, 0, W, H);
  ctx.save();
  if (fx.shake > 0) {
    ctx.translate((Math.random() - 0.5) * fx.shake, (Math.random() - 0.5) * fx.shake);
  }
  drawField(now);
  const snap = state.snapNow;
  if (snap) {
    drawPads(snap);
    drawLaneDominance(snap);
    drawAnomalies(snap, now);
    drawCaches(snap, now);
    drawBurns(snap, now);
    drawTurrets(snap, now);
    drawWalls(snap, now);
    drawUnits(snap, now);
    drawFogOfWar(snap);
    drawProjectiles();
    drawParticles();
    drawRings();
    drawFlashes(now);
    drawFloaters();
    if (snap.overtime) drawOvertimeTint();
  } else {
    drawIdleHint();
  }
  ctx.restore();
}

function drawField(now) {
  const bg = ctx.createLinearGradient(0, 0, 0, H);
  bg.addColorStop(0, '#111838');
  bg.addColorStop(0.5, '#0e1430');
  bg.addColorStop(1, '#0b1024');
  ctx.fillStyle = bg;
  ctx.fillRect(0, 0, W, H);

  for (let lane = 0; lane < 5; lane++) {
    const y = FIELD.y0 + lane * LANE_H;
    ctx.fillStyle = lane % 2 === 0 ? 'rgba(148,163,216,0.055)' : 'rgba(148,163,216,0.028)';
    ctx.fillRect(FIELD.x0, y, FIELD.x1 - FIELD.x0, LANE_H);
    // lane separator
    ctx.fillStyle = 'rgba(148,163,216,0.10)';
    ctx.fillRect(FIELD.x0, y, FIELD.x1 - FIELD.x0, 1);
    ctx.fillStyle = '#66729b';
    ctx.font = '700 12px "Segoe UI", system-ui, sans-serif';
    ctx.textAlign = 'center';
    ctx.fillText(String(lane + 1), FIELD.x0 - 30, y + LANE_H / 2 + 4);
    ctx.fillText(String(lane + 1), FIELD.x1 + 30, y + LANE_H / 2 + 4);
  }

  // Pulsing breach glow strips
  const pulse = 0.55 + 0.3 * Math.sin(now / 480);
  drawBreachStrip(FIELD.x0 - 26, '#38bdf8', 'BREACH', pulse);
  drawBreachStrip(FIELD.x1 + 2, '#fb7185', 'BREACH', pulse);

  // Center line
  ctx.strokeStyle = 'rgba(148,163,216,0.12)';
  ctx.setLineDash([4, 12]);
  ctx.beginPath();
  ctx.moveTo((FIELD.x0 + FIELD.x1) / 2, FIELD.y0);
  ctx.lineTo((FIELD.x0 + FIELD.x1) / 2, H - 14);
  ctx.stroke();
  ctx.setLineDash([]);

  // Hover-lane highlight (deployment aid)
  if (state.hoverLane != null && state.selection) {
    const y = FIELD.y0 + state.hoverLane * LANE_H;
    ctx.fillStyle = state.seat === 0 ? 'rgba(56,189,248,0.10)' : 'rgba(251,113,133,0.10)';
    ctx.fillRect(FIELD.x0, y, FIELD.x1 - FIELD.x0, LANE_H);
    ctx.strokeStyle = state.seat === 0 ? 'rgba(56,189,248,0.55)' : 'rgba(251,113,133,0.55)';
    ctx.lineWidth = 1.5;
    ctx.strokeRect(FIELD.x0 + 0.5, y + 0.5, FIELD.x1 - FIELD.x0 - 1, LANE_H - 1);
  }
}

function drawBreachStrip(x, color, label, alpha) {
  const g = ctx.createLinearGradient(x, 0, x + 24, 0);
  g.addColorStop(0, color + '00');
  g.addColorStop(0.5, color + Math.round(alpha * 255).toString(16).padStart(2, '0'));
  g.addColorStop(1, color + '00');
  ctx.fillStyle = g;
  ctx.fillRect(x, 0, 24, H);
  ctx.save();
  ctx.translate(x + 12, H / 2);
  ctx.rotate(-Math.PI / 2);
  ctx.fillStyle = color;
  ctx.globalAlpha = Math.min(1, alpha + 0.25);
  ctx.font = '800 13px "Segoe UI", system-ui, sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText(label.split('').join(' '), 0, 4);
  ctx.restore();
  ctx.globalAlpha = 1;
}

function drawPads(snap) {
  const padX = [10, 90]; // field units per side
  const turrets = snap.turrets || [];
  for (let lane = 0; lane < 5; lane++) {
    for (const side of [0, 1]) {
      for (let slot = 0; slot < TURRET_SLOT_OFF.length; slot++) {
        const occupied = turrets.some((t) => t.side === side && t.lane === lane && (t.slot || 0) === slot);
        if (occupied) continue;
        const p = px(padX[side]);
        const y = turretSlotY(lane, slot);
        ctx.save();
        ctx.setLineDash([5, 5]);
        ctx.strokeStyle = side === state.seat ? 'rgba(125,211,252,0.45)' : 'rgba(148,163,184,0.22)';
        ctx.lineWidth = 1.5;
        roundRect(ctx, p - 16, y - 15, 32, 30, 7);
        ctx.stroke();
        ctx.restore();
      }
    }
  }
}

function drawTurrets(snap, now) {
  // Lower slot drawn first so the upper slot's barrel/pips sit on top.
  const sorted = [...(snap.turrets || [])].sort((a, b) => (a.slot || 0) - (b.slot || 0));
  for (const t of sorted) {
    const p = px(t.side === 0 ? 10 : 90);
    const py = turretSlotY(t.lane, t.slot || 0);
    const trim = t.side === 0 ? '#7dd3fc' : '#fda4af';
    const body = t.side === 0 ? '#1e4fd8' : '#b91c3c';
    const bodyDark = t.side === 0 ? '#152f6e' : '#6f1526';

    // recoil: look for a fresh muzzle flash for this turret
    let recoil = 0;
    for (const f of fx.flashes) {
      if (Math.abs(f.x - p) < 2 && Math.abs(f.y - (py - 4)) < 2) {
        recoil = Math.max(0, (f.until - now) / 90) * 4;
      }
    }
    const dir = t.side === 0 ? 1 : -1;

    // shadow
    ctx.fillStyle = 'rgba(0,0,0,0.35)';
    ctx.beginPath();
    ctx.ellipse(p, py + 14, 20, 6, 0, 0, Math.PI * 2);
    ctx.fill();

    // base + body
    ctx.fillStyle = '#0f172a';
    roundRect(ctx, p - 17 - dir * recoil, py - 14, 34, 30, 6); ctx.fill();
    ctx.fillStyle = body;
    roundRect(ctx, p - 15 - dir * recoil, py - 16, 30, 30, 6); ctx.fill();
    ctx.fillStyle = bodyDark;
    roundRect(ctx, p - 15 - dir * recoil, py + 2, 30, 12, 6); ctx.fill();
    ctx.strokeStyle = trim;
    ctx.lineWidth = 2;
    roundRect(ctx, p - 15 - dir * recoil, py - 16, 30, 30, 6); ctx.stroke();

    // barrel
    ctx.strokeStyle = trim;
    ctx.lineWidth = 5;
    ctx.beginPath();
    ctx.moveTo(p - dir * recoil, py - 2);
    ctx.lineTo(p + dir * (18 - recoil), py - 2);
    ctx.stroke();

    // level pips on the dark lower plate
    for (let i = 0; i < t.level; i++) {
      ctx.fillStyle = '#fde047';
      ctx.beginPath();
      ctx.arc(p - 4 + i * 8, py + 8, 2.2, 0, Math.PI * 2);
      ctx.fill();
    }
    if (t.maxShield > 0) {
      drawShieldBar(p - 18, py + 14, 36, 3, t.shield / t.maxShield);
    }
    drawHealthBar(p - 18, py + 18, 36, 4, t.hp / t.maxHp);
  }
}

function drawWalls(snap, now) {
  for (const w of snap.walls || []) {
    const x = px(w.x);
    const y0 = FIELD.y0 + w.lane * LANE_H + 14;
    const h = LANE_H - 28;
    const dir = w.side === 0 ? 1 : -1;
    const body = w.side === 0 ? '#1e40af' : '#9f1239';
    const trim = w.side === 0 ? '#7dd3fc' : '#fda4af';

    ctx.save();
    // Shadow
    ctx.fillStyle = 'rgba(0,0,0,0.38)';
    roundRect(ctx, x - 9, y0 + 2, 18, h, 4); ctx.fill();
    // Stone wall body
    ctx.fillStyle = body;
    roundRect(ctx, x - 7, y0, 14, h, 4); ctx.fill();
    ctx.strokeStyle = trim;
    ctx.lineWidth = 2;
    roundRect(ctx, x - 7, y0, 14, h, 4); ctx.stroke();

    // Crenellations / masonry bricks
    ctx.fillStyle = 'rgba(255,255,255,0.16)';
    for (let sy = y0 + 8; sy < y0 + h - 8; sy += 16) {
      ctx.fillRect(x - 4, sy, 8, 6);
    }
    // Iron spikes if upgraded (level >= 1)
    if (w.level > 0) {
      ctx.fillStyle = w.level >= 2 ? '#fde047' : '#cbd5e1';
      for (let sy = y0 + 10; sy < y0 + h - 6; sy += 14) {
        ctx.beginPath();
        ctx.moveTo(x + dir * 7, sy - 4);
        ctx.lineTo(x + dir * (12 + w.level * 2), sy);
        ctx.lineTo(x + dir * 7, sy + 4);
        ctx.closePath();
        ctx.fill();
      }
    }
    ctx.restore();

    const midY = y0 + h / 2;
    if (w.maxShield > 0) {
      drawShieldBar(x - 16, midY + h / 2 + 2, 32, 3, w.shield / w.maxShield);
    }
    drawHealthBar(x - 16, midY + h / 2 + 6, 32, 4, w.hp / w.maxHp);
  }
}

function drawLaneDominance(snap) {
  const dom = snap.laneDominance || [];
  const midX = (FIELD.x0 + FIELD.x1) / 2;
  for (let lane = 0; lane < 5; lane++) {
    const side = dom[lane];
    if (side !== 0 && side !== 1) continue;
    const y = FIELD.y0 + lane * LANE_H + 10;
    ctx.save();
    ctx.fillStyle = side === 0 ? 'rgba(56,189,248,0.85)' : 'rgba(251,113,133,0.85)';
    ctx.beginPath();
    ctx.moveTo(midX, y);
    ctx.lineTo(midX + (side === 0 ? 12 : -12), y + 4);
    ctx.lineTo(midX, y + 8);
    ctx.closePath();
    ctx.fill();
    ctx.strokeStyle = '#e2e8f0';
    ctx.lineWidth = 1.4;
    ctx.beginPath(); ctx.moveTo(midX, y); ctx.lineTo(midX, y + 14); ctx.stroke();
    ctx.restore();
  }
}

function drawAnomalies(snap, now) {
  for (const a of snap.anomalies || []) {
    const x = px(a.x);
    const y = laneY(a.lane);
    const bob = Math.sin(now / 360 + a.lane) * 2.5;
    ctx.save();
    if (a.kind === 'loot_coins') {
      ctx.fillStyle = a.contested ? 'rgba(248, 113, 113, 0.25)' : 'rgba(251, 191, 36, 0.20)';
      ctx.beginPath(); ctx.arc(x, y, 22, 0, Math.PI * 2); ctx.fill();
      ctx.fillStyle = '#f59e0b';
      roundRect(ctx, x - 11, y - 9 + bob, 22, 18, 4); ctx.fill();
      ctx.strokeStyle = a.contested ? '#fca5a5' : '#fef08a'; ctx.lineWidth = 1.5;
      roundRect(ctx, x - 11, y - 9 + bob, 22, 18, 4); ctx.stroke();
      ctx.fillStyle = '#fffbeb'; ctx.font = '800 11px "Segoe UI", sans-serif'; ctx.textAlign = 'center';
      ctx.fillText(`+${a.value}c`, x, y + 4 + bob);
      if (a.contested) {
        ctx.fillStyle = '#fca5a5'; ctx.font = '800 9px "Segoe UI", sans-serif';
        ctx.fillText('⚔️ CONTESTED', x, y - 15 + bob);
      }
    } else if (a.kind === 'loot_buff') {
      ctx.fillStyle = 'rgba(56, 189, 248, 0.20)';
      ctx.beginPath(); ctx.arc(x, y, 22, 0, Math.PI * 2); ctx.fill();
      ctx.fillStyle = '#0284c7';
      ctx.beginPath(); ctx.moveTo(x, y - 14 + bob); ctx.lineTo(x + 12, y + 8 + bob); ctx.lineTo(x - 12, y + 8 + bob); ctx.closePath(); ctx.fill();
      ctx.strokeStyle = '#7dd3fc'; ctx.lineWidth = 1.5; ctx.stroke();
      ctx.fillStyle = '#e0f2fe'; ctx.font = '800 9px "Segoe UI", sans-serif'; ctx.textAlign = 'center';
      ctx.fillText('SHRINE', x, y + 20);
    } else if (a.kind === 'neutral_golem') {
      ctx.fillStyle = 'rgba(168, 85, 247, 0.18)';
      ctx.beginPath(); ctx.arc(x, y, 24, 0, Math.PI * 2); ctx.fill();
      ctx.fillStyle = '#581c87';
      ctx.strokeStyle = '#d8b4fe'; ctx.lineWidth = 2;
      ctx.beginPath(); ctx.arc(x, y + bob * 0.5, 13, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
      ctx.fillStyle = '#fde047';
      ctx.beginPath(); ctx.arc(x - 4, y - 3 + bob * 0.5, 2, 0, Math.PI * 2); ctx.arc(x + 4, y - 3 + bob * 0.5, 2, 0, Math.PI * 2); ctx.fill();
      drawHealthBar(x - 16, y - 22, 32, 4, a.hp / (a.maxHp || 1));
      ctx.fillStyle = '#fde047'; ctx.font = '700 9px "Segoe UI", sans-serif'; ctx.textAlign = 'center';
      ctx.fillText(`👹 +${a.value}c`, x, y + 23);
    } else if (a.kind === 'hazard_fire') {
      ctx.fillStyle = 'rgba(239, 68, 68, 0.22)';
      ctx.beginPath(); ctx.ellipse(x, y, 28, 18, 0, 0, Math.PI * 2); ctx.fill();
      ctx.strokeStyle = 'rgba(249, 115, 22, 0.7)'; ctx.lineWidth = 1.5; ctx.stroke();
      ctx.fillStyle = '#fdba74'; ctx.font = '700 10px "Segoe UI", sans-serif'; ctx.textAlign = 'center';
      ctx.fillText('🔥 FIRE', x, y + 4);
    } else if (a.kind === 'hazard_tar') {
      ctx.fillStyle = 'rgba(30, 41, 59, 0.75)';
      ctx.beginPath(); ctx.ellipse(x, y, 30, 18, 0, 0, Math.PI * 2); ctx.fill();
      ctx.strokeStyle = 'rgba(148, 163, 184, 0.45)'; ctx.lineWidth = 1.5; ctx.stroke();
      ctx.fillStyle = '#cbd5e1'; ctx.font = '700 10px "Segoe UI", sans-serif'; ctx.textAlign = 'center';
      ctx.fillText('🕸️ TAR', x, y + 4);
    } else if (a.kind === 'hazard_mud') {
      const rx = Math.max(24, ((a.radius || 5) / 100) * (FIELD.x1 - FIELD.x0));
      ctx.fillStyle = 'rgba(92, 64, 51, 0.78)';
      ctx.beginPath(); ctx.ellipse(x, y, rx, 26, 0, 0, Math.PI * 2); ctx.fill();
      ctx.strokeStyle = 'rgba(161, 122, 91, 0.65)'; ctx.lineWidth = 1.6; ctx.stroke();
      ctx.fillStyle = '#e7d5c3'; ctx.font = '700 10px "Segoe UI", sans-serif'; ctx.textAlign = 'center';
      ctx.fillText('🟤 MUD', x, y + 4);
    } else if (a.kind === 'hazard_spikes') {
      const rx = Math.max(24, ((a.radius || 5) / 100) * (FIELD.x1 - FIELD.x0));
      ctx.fillStyle = 'rgba(127, 29, 29, 0.30)';
      roundRect(ctx, x - rx, y - 22, rx * 2, 44, 8); ctx.fill();
      ctx.strokeStyle = 'rgba(248, 113, 113, 0.6)'; ctx.lineWidth = 1.4;
      roundRect(ctx, x - rx, y - 22, rx * 2, 44, 8); ctx.stroke();
      ctx.fillStyle = '#cbd5e1';
      const step = 12;
      for (let sx = x - rx + 8; sx <= x + rx - 8; sx += step) {
        ctx.beginPath(); ctx.moveTo(sx - 4, y + 10); ctx.lineTo(sx, y - 10); ctx.lineTo(sx + 4, y + 10); ctx.closePath(); ctx.fill();
      }
      ctx.fillStyle = '#fecaca'; ctx.font = '800 10px "Segoe UI", sans-serif'; ctx.textAlign = 'center';
      ctx.fillText('🩸 SPIKES', x, y + 20);
    }
    ctx.restore();
  }
}

function drawFogOfWar(snap) {
  if (snap.phase !== 'prep') return;
  const foeLeft = state.seat === 0;
  const xStart = foeLeft ? (FIELD.x0 + FIELD.x1) / 2 : FIELD.x0;
  const width = (FIELD.x1 - FIELD.x0) / 2;
  const scouted = snap.scoutedLanes || [];
  ctx.save();
  for (let lane = 0; lane < 5; lane++) {
    const ly = FIELD.y0 + lane * LANE_H;
    if (scouted[lane]) {
      ctx.fillStyle = 'rgba(56, 189, 248, 0.14)';
      ctx.font = '700 11px "Segoe UI", system-ui, sans-serif';
      ctx.textAlign = 'center';
      ctx.fillStyle = 'rgba(125, 211, 252, 0.75)';
      ctx.fillText('🔭 SCOUTED', xStart + width / 2, ly + 18);
      continue;
    }
    ctx.fillStyle = 'rgba(10, 15, 34, 0.52)';
    ctx.fillRect(xStart, ly, width, LANE_H);
  }
  ctx.fillStyle = 'rgba(148, 163, 216, 0.55)';
  ctx.font = '700 14px "Segoe UI", system-ui, sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText('🌫️ FOG OF WAR — Enemy Preparing', xStart + width / 2, FIELD.y0 + LANE_H * 2.5);
  if (snap.hideLuck && (snap.hiddenLuckCount || 0) > 0) {
    ctx.fillStyle = 'rgba(251, 191, 36, 0.85)';
    ctx.font = '700 12px "Segoe UI", system-ui, sans-serif';
    ctx.fillText(`🎲 Luck Hidden (${snap.hiddenLuckCount} field item${snap.hiddenLuckCount > 1 ? 's' : ''} concealed until Combat)`, (FIELD.x0 + FIELD.x1) / 2, FIELD.y0 + 18);
  }
  ctx.restore();
}

function drawShieldBar(x, y, w, h, frac) {
  frac = Math.max(0, Math.min(1, frac));
  ctx.fillStyle = 'rgba(2,6,23,0.75)';
  ctx.fillRect(x - 1, y - 1, w + 2, h + 2);
  ctx.fillStyle = '#38bdf8';
  ctx.fillRect(x, y, w * frac, h);
}

function drawUnits(snap, now) {
  for (const u of snap.units || []) {
    const ux = px(unitRenderX(u));
    const uy = laneY(u.lane) + (u.yOff || 0) * LANE_H;
    const body = u.side === 0 ? '#38bdf8' : '#fb7185';
    const dark = u.side === 0 ? '#0c4a6e' : '#881337';
    const dir = u.side === 0 ? 1 : -1;

    // walk bob (dragons hover instead of walking)
    const prev = state.snapPrev && state.snapPrev.units.find((p) => p.id === u.id);
    const moving = prev ? Math.abs(u.x - prev.x) > 0.005 : true;
    const bob = moving ? Math.sin(now * 0.012 + u.id * 1.7) * 1.8 : 0;
    const hover = u.type === 'dragon' ? Math.sin(now * 0.0045 + u.id * 1.7) * 3.5 - 9 : bob;

    // shadow
    ctx.fillStyle = u.type === 'dragon' ? 'rgba(0,0,0,0.22)' : 'rgba(0,0,0,0.32)';
    ctx.beginPath();
    ctx.ellipse(ux, uy + 12, u.type === 'dragon' ? 15 : 13, 4, 0, 0, Math.PI * 2);
    ctx.fill();

    ctx.save();
    ctx.translate(ux, uy + hover);
    ctx.scale(dir, 1);
    ctx.lineWidth = 2;
    ctx.strokeStyle = dark;
    ctx.fillStyle = body;

    switch (u.type) {
      case 'soldier':
        ctx.beginPath(); ctx.arc(0, 0, 9.5, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        ctx.fillStyle = dark;
        ctx.beginPath(); ctx.arc(1.5, -2, 5, Math.PI, 0); ctx.fill();
        // spear
        ctx.strokeStyle = '#e2e8f0'; ctx.lineWidth = 1.6;
        ctx.beginPath(); ctx.moveTo(4, 3); ctx.lineTo(11, -7); ctx.stroke();
        break;
      case 'bowman':
        ctx.beginPath(); ctx.arc(0, 0, 9, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        ctx.fillStyle = dark;
        ctx.beginPath(); ctx.arc(0, -3, 4.4, Math.PI, 0); ctx.fill();
        // bow
        ctx.strokeStyle = '#fbbf24'; ctx.lineWidth = 2;
        ctx.beginPath(); ctx.arc(7, 0, 8, -Math.PI / 2.3, Math.PI / 2.3); ctx.stroke();
        ctx.strokeStyle = '#e7e5e4'; ctx.lineWidth = 1;
        ctx.beginPath(); ctx.moveTo(9.2, -6.4); ctx.lineTo(9.2, 6.4); ctx.stroke();
        break;
      case 'knight':
        ctx.beginPath();
        ctx.moveTo(0, -12); ctx.lineTo(11, 0); ctx.lineTo(0, 12); ctx.lineTo(-11, 0);
        ctx.closePath(); ctx.fill(); ctx.stroke();
        ctx.fillStyle = dark;
        ctx.beginPath(); ctx.arc(0, 0, 4.2, 0, Math.PI * 2); ctx.fill();
        // lance
        ctx.strokeStyle = '#e2e8f0'; ctx.lineWidth = 1.6;
        ctx.beginPath(); ctx.moveTo(6, 2); ctx.lineTo(15, -6); ctx.stroke();
        break;
      case 'shieldbearer':
        // heavy rectangular shield + armored body
        ctx.beginPath(); ctx.arc(-2, 0, 10, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        ctx.fillStyle = '#94a3b8'; ctx.strokeStyle = '#0f172a'; ctx.lineWidth = 2;
        roundRect(ctx, 5, -11, 6, 22, 2); ctx.fill(); ctx.stroke();
        break;
      case 'mage':
        // hooded caster + glowing orb
        ctx.beginPath(); ctx.moveTo(-8, 10); ctx.lineTo(0, -11); ctx.lineTo(8, 10); ctx.closePath(); ctx.fill(); ctx.stroke();
        ctx.fillStyle = '#c084fc';
        ctx.beginPath(); ctx.arc(9, -4, 4, 0, Math.PI * 2); ctx.fill();
        break;
      case 'valkyrie': {
        const vflap = Math.sin(now * 0.015 + u.id * 1.3) * 4;
        ctx.fillStyle = '#fde047';
        ctx.beginPath(); ctx.moveTo(-2, -3); ctx.lineTo(-14, -14 - vflap); ctx.lineTo(-8, 1); ctx.closePath(); ctx.fill();
        ctx.fillStyle = body;
        ctx.beginPath(); ctx.arc(0, 0, 9.5, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        ctx.strokeStyle = '#fef08a'; ctx.lineWidth = 2.2;
        ctx.beginPath(); ctx.moveTo(4, 2); ctx.lineTo(15, -8); ctx.stroke();
        break;
      }
      case 'ram':
        ctx.fillStyle = '#b45309';
        roundRect(ctx, -17, -9, 24, 18, 3); ctx.fill();
        ctx.strokeStyle = '#451a03';
        ctx.lineWidth = 2;
        roundRect(ctx, -17, -9, 24, 18, 3); ctx.stroke();
        // ram head
        ctx.fillStyle = '#d6d3d1';
        ctx.beginPath();
        ctx.moveTo(7, -8); ctx.lineTo(20, 0); ctx.lineTo(7, 8); ctx.closePath(); ctx.fill(); ctx.stroke();
        // wheels
        ctx.fillStyle = '#1c1917';
        ctx.beginPath(); ctx.arc(-9, 9, 3.6, 0, Math.PI * 2); ctx.arc(1, 9, 3.6, 0, Math.PI * 2); ctx.fill();
        break;
      case 'siege':
        ctx.beginPath();
        ctx.moveTo(-15, 8); ctx.lineTo(-10, -8); ctx.lineTo(10, -8); ctx.lineTo(15, 8);
        ctx.closePath(); ctx.fill(); ctx.stroke();
        // barrel
        ctx.strokeStyle = '#e2e8f0'; ctx.lineWidth = 3.4;
        ctx.beginPath(); ctx.moveTo(2, -3); ctx.lineTo(20, -9); ctx.stroke();
        // wheels
        ctx.fillStyle = '#0f172a';
        ctx.beginPath(); ctx.arc(-9, 8.5, 4, 0, Math.PI * 2); ctx.arc(9, 8.5, 4, 0, Math.PI * 2); ctx.fill();
        ctx.fillStyle = '#64748b';
        ctx.beginPath(); ctx.arc(-9, 8.5, 1.6, 0, Math.PI * 2); ctx.arc(9, 8.5, 1.6, 0, Math.PI * 2); ctx.fill();
        break;
      case 'dragon': {
        const wing = u.side === 0 ? '#38bdf8' : '#fb7185';
        const flap = Math.sin(now * 0.011 + u.id * 1.7);
        // far wing
        ctx.fillStyle = 'rgba(109,40,217,0.85)';
        ctx.beginPath();
        ctx.moveTo(-1, -5);
        ctx.quadraticCurveTo(-8, -18 - flap * 6, -21, -12 - flap * 8);
        ctx.lineTo(-5, 1);
        ctx.closePath(); ctx.fill();
        // tail with flame tip
        const wag = Math.sin(now * 0.006 + u.id) * 3;
        ctx.strokeStyle = '#6d28d9'; ctx.lineWidth = 4; ctx.lineCap = 'round';
        ctx.beginPath(); ctx.moveTo(-11, 1); ctx.quadraticCurveTo(-19, 3 + wag, -27, -2 + wag * 1.6); ctx.stroke();
        ctx.fillStyle = '#f97316';
        ctx.beginPath();
        const tailTipY = -2 + wag * 1.6;
        ctx.moveTo(-27, tailTipY - 3); ctx.lineTo(-33, tailTipY); ctx.lineTo(-27, tailTipY + 3);
        ctx.closePath(); ctx.fill();
        // body
        ctx.fillStyle = '#7c3aed';
        ctx.strokeStyle = '#2e1065'; ctx.lineWidth = 2;
        ctx.beginPath(); ctx.ellipse(0, 0, 13, 7.4, -0.06, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        // belly
        ctx.fillStyle = '#fde68a';
        ctx.beginPath(); ctx.ellipse(1.5, 2.6, 8.5, 3.6, 0, 0, Math.PI); ctx.fill();
        // back spikes
        ctx.fillStyle = '#fbbf24';
        for (const sx of [-6, 0, 6]) {
          ctx.beginPath(); ctx.moveTo(sx - 2.2, -5.4); ctx.lineTo(sx, -10.5); ctx.lineTo(sx + 2.2, -5.4); ctx.closePath(); ctx.fill();
        }
        // legs tucked
        ctx.strokeStyle = '#4c1d95'; ctx.lineWidth = 2.4;
        ctx.beginPath(); ctx.moveTo(-4, 6); ctx.lineTo(-6, 10.5); ctx.moveTo(6, 6); ctx.lineTo(8, 10.5); ctx.stroke();
        // head
        ctx.fillStyle = '#7c3aed';
        ctx.strokeStyle = '#2e1065'; ctx.lineWidth = 2;
        ctx.beginPath(); ctx.arc(12.5, -6, 5.6, 0, Math.PI * 2); ctx.fill(); ctx.stroke();
        // snout
        ctx.beginPath(); ctx.moveTo(15.5, -9.2); ctx.lineTo(23.5, -5.4); ctx.lineTo(15.5, -2.6); ctx.closePath(); ctx.fill(); ctx.stroke();
        // horns
        ctx.fillStyle = '#fde68a';
        ctx.beginPath(); ctx.moveTo(9.5, -10.2); ctx.lineTo(7.5, -15.5); ctx.lineTo(12.5, -11.4); ctx.closePath(); ctx.fill();
        ctx.beginPath(); ctx.moveTo(13.5, -10.8); ctx.lineTo(12.5, -16); ctx.lineTo(16.5, -11.2); ctx.closePath(); ctx.fill();
        // eye (molten)
        ctx.save();
        ctx.shadowColor = '#fbbf24'; ctx.shadowBlur = 6;
        ctx.fillStyle = '#fde047';
        ctx.beginPath(); ctx.arc(13.6, -6.6, 1.35, 0, Math.PI * 2); ctx.fill();
        ctx.restore();
        // near wing
        ctx.fillStyle = wing;
        ctx.globalAlpha = 0.9;
        ctx.beginPath();
        ctx.moveTo(1, -5);
        ctx.quadraticCurveTo(-4, -24 - flap * 7, -19, -17 - flap * 9);
        ctx.lineTo(-3, 1);
        ctx.closePath(); ctx.fill();
        ctx.globalAlpha = 1;
        ctx.strokeStyle = '#2e1065'; ctx.lineWidth = 1.4;
        ctx.stroke();
        break;
      }
    }
    ctx.restore();

    if (u.type === 'dragon') {
      if (u.maxShield > 0) drawShieldBar(ux - 17, uy - 39, 34, 3.2, u.shield / u.maxShield);
      drawHealthBar(ux - 17, uy - 34, 34, 4.2, u.hp / u.maxHp);
    } else {
      if (u.maxShield > 0) drawShieldBar(ux - 13, uy - 22, 26, 2.8, u.shield / u.maxShield);
      drawHealthBar(ux - 13, uy - 18, 26, 3.6, u.hp / u.maxHp);
    }
  }
}

function drawProjectiles() {
  for (const p of fx.projectiles) {
    const pos = projPos(p, p.t);
    if (p.kind === 'bullet') {
      ctx.save();
      ctx.shadowColor = '#fef08a';
      ctx.shadowBlur = 6;
      ctx.fillStyle = '#fef9c3';
      ctx.beginPath();
      ctx.arc(pos.x, pos.y, 2.4, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    } else if (p.kind === 'arrow') {
      const angle = Math.atan2(
        (projPos(p, Math.min(1, p.t + 0.02)).y) - pos.y,
        (projPos(p, Math.min(1, p.t + 0.02)).x) - pos.x
      );
      ctx.save();
      ctx.translate(pos.x, pos.y);
      ctx.rotate(angle);
      ctx.strokeStyle = '#e7c576';
      ctx.lineWidth = 2;
      ctx.beginPath(); ctx.moveTo(-7, 0); ctx.lineTo(6, 0); ctx.stroke();
      ctx.beginPath(); ctx.moveTo(6, 0); ctx.lineTo(2, -3); ctx.moveTo(6, 0); ctx.lineTo(2, 3); ctx.stroke();
      ctx.restore();
    } else if (p.kind === 'shell') {
      ctx.save();
      ctx.shadowColor = '#fb923c';
      ctx.shadowBlur = 8;
      ctx.fillStyle = '#f8fafc';
      ctx.beginPath();
      ctx.arc(pos.x, pos.y, 4.5, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    } else if (p.kind === 'fireball') {
      ctx.save();
      ctx.shadowColor = '#fb923c';
      ctx.shadowBlur = 12;
      const g = ctx.createRadialGradient(pos.x, pos.y, 0, pos.x, pos.y, 6);
      g.addColorStop(0, '#fef08a');
      g.addColorStop(0.45, '#fb923c');
      g.addColorStop(1, 'rgba(239,68,68,0)');
      ctx.fillStyle = g;
      ctx.beginPath();
      ctx.arc(pos.x, pos.y, 6, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    }
  }
}

function drawParticles() {
  for (const p of fx.particles) {
    ctx.globalAlpha = Math.max(0, p.life / p.maxLife);
    ctx.fillStyle = p.color;
    ctx.beginPath();
    ctx.arc(p.x, p.y, p.size, 0, Math.PI * 2);
    ctx.fill();
  }
  ctx.globalAlpha = 1;
}

function drawRings() {
  for (const r of fx.rings) {
    ctx.globalAlpha = Math.max(0, r.life / r.maxLife) * 0.9;
    ctx.strokeStyle = '#fdba74';
    ctx.lineWidth = 3;
    ctx.beginPath();
    ctx.arc(r.x, r.y, r.r, 0, Math.PI * 2);
    ctx.stroke();
  }
  ctx.globalAlpha = 1;
}

function drawFlashes(now) {
  for (const f of fx.flashes) {
    const a = Math.max(0, (f.until - now) / 110);
    const g = ctx.createRadialGradient(f.x, f.y, 0, f.x, f.y, f.r);
    g.addColorStop(0, f.color);
    g.addColorStop(1, 'rgba(255,255,255,0)');
    ctx.globalAlpha = a;
    ctx.fillStyle = g;
    ctx.beginPath();
    ctx.arc(f.x, f.y, f.r, 0, Math.PI * 2);
    ctx.fill();
  }
  ctx.globalAlpha = 1;
}

function drawFloaters() {
  ctx.textAlign = 'center';
  ctx.font = '800 12px "Segoe UI", system-ui, sans-serif';
  for (const f of fx.floaters) {
    ctx.globalAlpha = Math.max(0, f.life / f.maxLife);
    ctx.lineWidth = 3;
    ctx.strokeStyle = 'rgba(2,6,23,0.8)';
    ctx.strokeText(f.text, f.x, f.y);
    ctx.fillStyle = f.color;
    ctx.fillText(f.text, f.x, f.y);
  }
  ctx.globalAlpha = 1;
}

function drawHealthBar(x, y, w, h, frac) {
  frac = Math.max(0, Math.min(1, frac));
  ctx.fillStyle = 'rgba(2,6,23,0.75)';
  ctx.fillRect(x - 1, y - 1, w + 2, h + 2);
  ctx.fillStyle = frac > 0.55 ? '#34d399' : frac > 0.25 ? '#facc15' : '#f87171';
  ctx.fillRect(x, y, w * frac, h);
}

function drawOvertimeTint() {
  ctx.fillStyle = 'rgba(220, 38, 38, 0.06)';
  ctx.fillRect(0, 0, W, H);
  ctx.strokeStyle = 'rgba(248, 113, 113, 0.6)';
  ctx.lineWidth = 4;
  ctx.strokeRect(2, 2, W - 4, H - 4);
}

function drawIdleHint() {
  ctx.fillStyle = 'rgba(226,232,240,0.5)';
  ctx.font = '600 18px "Segoe UI", system-ui, sans-serif';
  ctx.textAlign = 'center';
  ctx.fillText('Waiting for the battle to begin…', W / 2, H / 2);
}

function roundRect(ctx, x, y, w, h, r) {
  ctx.beginPath();
  ctx.moveTo(x + r, y);
  ctx.arcTo(x + w, y, x + w, y + h, r);
  ctx.arcTo(x + w, y + h, x, y + h, r);
  ctx.arcTo(x, y + h, x, y, r);
  ctx.arcTo(x, y, x + w, y, r);
  ctx.closePath();
}

// ---------------------------------------------------------------------------
// Windfall banner + confetti + toast
// ---------------------------------------------------------------------------
let bannerTimer = null;
function showBanner(text, variant) {
  els.windfall.textContent = text;
  els.windfall.className = 'windfall-banner pop ' + (variant || 'gold');
  clearTimeout(bannerTimer);
  bannerTimer = setTimeout(() => els.windfall.classList.add('hidden'), 2400);
}

function showToast(msg, ok) {
  els.toast.textContent = msg;
  els.toast.classList.toggle('ok', !!ok);
  els.toast.classList.remove('hidden');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => els.toast.classList.add('hidden'), 2800);
}
let toastTimer = null;

const confetti = { running: false, parts: [] };
const CONFETTI_COLORS = ['#38bdf8', '#f43f5e', '#fbbf24', '#34d399', '#e879f9', '#f8fafc', '#fda4af'];

function launchConfetti() {
  const c = confettiCv;
  const dpr = window.devicePixelRatio || 1;
  c.width = window.innerWidth * dpr;
  c.height = window.innerHeight * dpr;
  c.style.width = window.innerWidth + 'px';
  c.style.height = window.innerHeight + 'px';
  fctx.setTransform(dpr, 0, 0, dpr, 0, 0);
  c.classList.remove('hidden');
  const Wpx = window.innerWidth;
  for (let i = 0; i < 170; i++) {
    confetti.parts.push({
      x: Math.random() * Wpx,
      y: -20 - Math.random() * window.innerHeight * 0.55,
      vx: (Math.random() - 0.5) * 110,
      vy: 80 + Math.random() * 150,
      w: 6 + Math.random() * 8,
      h: 8 + Math.random() * 12,
      rot: Math.random() * Math.PI,
      vr: (Math.random() - 0.5) * 6,
      sway: Math.random() * Math.PI * 2,
      color: CONFETTI_COLORS[Math.floor(Math.random() * CONFETTI_COLORS.length)],
    });
  }
  if (!confetti.running) {
    confetti.running = true;
    let last = performance.now();
    const tick = (now) => {
      const dt = Math.min(0.05, (now - last) / 1000);
      last = now;
      fctx.clearRect(0, 0, window.innerWidth, window.innerHeight);
      let alive = 0;
      for (const p of confetti.parts) {
        p.sway += dt * 2.2;
        p.x += (p.vx + Math.sin(p.sway) * 35) * dt;
        p.y += p.vy * dt;
        p.rot += p.vr * dt;
        if (p.y > window.innerHeight + 30) continue;
        alive++;
        fctx.save();
        fctx.translate(p.x, p.y);
        fctx.rotate(p.rot);
        fctx.fillStyle = p.color;
        fctx.fillRect(-p.w / 2, -p.h / 2, p.w, p.h);
        fctx.restore();
      }
      if (alive > 0) {
        requestAnimationFrame(tick);
      } else {
        confetti.running = false;
        confetti.parts = [];
        fctx.clearRect(0, 0, window.innerWidth, window.innerHeight);
        c.classList.add('hidden');
      }
    };
    requestAnimationFrame(tick);
  }
}

// ---------------------------------------------------------------------------
// Lobby flow
// ---------------------------------------------------------------------------
function showScreen(name) {
  state.screen = name;
  els.lobby.classList.toggle('hidden', name !== 'lobby');
  els.game.classList.toggle('hidden', name !== 'game');
}

async function enterGame(code, token, seat) {
  state.code = code;
  state.seat = seat;
  sessionToken = token;
  SS.save(code, token, seat);
  els.roomCode.textContent = code;
  els.waitingCode.textContent = code;
  els.waiting.classList.remove('hidden');
  els.gameover.classList.add('hidden');
  showScreen('game');
  connectWS(code, token);
}

async function onCreate() {
  els.lobbyError.textContent = '';
  SFX.click();
  try {
    const hideLuck = $('room-hide-luck') ? $('room-hide-luck').checked : true;
    const res = await api('/api/rooms', { ...couponPayload(), lives: Number($('room-lives').value), hideLuck });
    await enterGame(res.code, res.token, res.seat);
  } catch (e) {
    els.lobbyError.textContent = e.message;
  }
}

async function onJoin(ev) {
  ev.preventDefault();
  els.lobbyError.textContent = '';
  SFX.click();
  const code = $('join-code').value.trim().toUpperCase();
  if (code.length !== 4) {
    els.lobbyError.textContent = 'Room codes are 4 letters.';
    return;
  }
  try {
    const res = await api(`/api/rooms/${encodeURIComponent(code)}/join`, couponPayload());
    await enterGame(res.code, res.token, res.seat);
  } catch (e) {
    els.lobbyError.textContent = e.message;
  }
}

async function tryResume() {
  refreshCouponStatus();
  const saved = SS.load();
  if (!saved) return;
  try {
    const res = await api(`/api/rooms/${encodeURIComponent(saved.code)}/rejoin`, Object.assign(couponPayload(), { token: saved.token }));
    await enterGame(res.code, res.token, res.seat);
  } catch {
    SS.clear();
  }
}

async function backToMenu() {
  if (!state.over && state.screen === 'game' && els.waiting.classList.contains('hidden')) {
    if (!confirm('Leave and forfeit this match?')) return;
  }
  if (state.code && sessionToken) {
    try { await api(`/api/rooms/${encodeURIComponent(state.code)}/leave`, { token: sessionToken }); } catch (e) { showToast(e.message, false); return; }
  }
  SS.clear();
  SFX.stopMusic();
  if (state.ws) { try { state.ws.close(); } catch {} }
  state.ws = null;
  state.snapNow = null;
  state.snapPrev = null;
  state.over = false;
  state.feed = [];
  fx.hpCache.clear();
  fx.projectiles.length = 0;
  fx.particles.length = 0;
  fx.floaters.length = 0;
  fx.rings.length = 0;
  fx.flashes.length = 0;
  fx.shake = 0;
  els.feed.innerHTML = '';
  els.disconnect.classList.add('hidden');
  setSidebar(false);
  els.shopToggle.classList.remove('hidden');
  resetResignButton();
  els.resignBtn.disabled = false;
  SFX.click();
  refreshCouponStatus();
  showScreen('lobby');
  if (document.fullscreenElement && document.exitFullscreen) {
    try { await document.exitFullscreen(); } catch { /* Browser may already have exited fullscreen. */ }
  }
}

// ---------------------------------------------------------------------------
// Input wiring
// ---------------------------------------------------------------------------
const fullscreenBtn = $('btn-fullscreen');
const isIPhone = /iPhone|iPod/.test(navigator.userAgent);
const isStandalone = () => window.matchMedia('(display-mode: standalone)').matches || navigator.standalone === true;
const showFullscreenHelp = () => { $('fullscreen-help').classList.remove('hidden'); $('fullscreen-help-close').focus(); };
function updateFullscreenButton() {
  const active = !!document.fullscreenElement;
  const available = !!(document.fullscreenEnabled && document.documentElement.requestFullscreen);
  const help = !available && isIPhone && !isStandalone();
  const label = help ? 'Fullscreen instructions' : active ? 'Exit fullscreen' : 'Enter fullscreen';
  fullscreenBtn.classList.toggle('hidden', !active && !available && !help);
  fullscreenBtn.setAttribute('aria-label', label);
  fullscreenBtn.title = label;
  fullscreenBtn.setAttribute('aria-pressed', String(active));
}
fullscreenBtn.addEventListener('click', async () => {
  if (!document.fullscreenEnabled || !document.documentElement.requestFullscreen) {
    if (isIPhone && !isStandalone()) showFullscreenHelp();
    return;
  }
  try {
    if (document.fullscreenElement) await document.exitFullscreen();
    else await document.documentElement.requestFullscreen();
  } catch {
    if (isIPhone && !isStandalone()) showFullscreenHelp();
    else showToast('Fullscreen is unavailable', false);
  }
});
$('fullscreen-help-close').addEventListener('click', () => {
  $('fullscreen-help').classList.add('hidden');
  fullscreenBtn.focus();
});
document.addEventListener('fullscreenchange', updateFullscreenButton);
updateFullscreenButton();

function laneFromEvent(ev) {
  const rect = cv.getBoundingClientRect();
  const y = (ev.clientY - rect.top) * (H / rect.height);
  const lane = Math.floor((y - FIELD.y0) / LANE_H);
  return lane >= 0 && lane < 5 ? lane : null;
}

$('btn-create').addEventListener('click', onCreate);
$('join-form').addEventListener('submit', onJoin);
$('btn-quit').addEventListener('click', backToMenu);
$('btn-without-coupon').addEventListener('click', () => {
  if (!send({ type: 'withoutCoupon' })) return;
  CouponStore.clear();
  updateCouponRemoveButton();
  $('btn-without-coupon').classList.add('hidden');
});
$('coupon-remove').addEventListener('click', () => {
  CouponStore.clear();
  updateCouponRemoveButton();
  els.couponStatus.textContent = '';
});
$('btn-back-menu').addEventListener('click', backToMenu);
$('btn-rematch').addEventListener('click', () => {
  if (state.rematchPending) return;
  SFX.click();
  state.rematchPending = true;
  els.rematchBtn.disabled = true;
  els.rematchBtn.textContent = 'Waiting…';
  els.rematchStatus.textContent = 'Rematch requested — waiting for opponent…';
  send({ type: 'rematch' });
});

// Quick emotes — send a reaction, see it bubble over your side of the field.
const EMOTES = { nice: '👏', gg: '🤝', lol: '😂', ouch: '😖', wow: '😱' };
function emoteGlyph(id) { return EMOTES[id] || '❔'; }
let lastEmoteSent = 0;
function showEmoteBubble(seat, emote) {
  const wrap = $('canvas-wrap');
  const div = document.createElement('div');
  div.className = 'emote-bubble ' + (seat === state.seat ? 'mine' : 'foe');
  div.textContent = emoteGlyph(emote);
  wrap.appendChild(div);
  setTimeout(() => div.remove(), 1900);
}
if (els.emoteBar) {
  els.emoteBar.addEventListener('click', (e) => {
    const btn = e.target.closest('.emote-btn');
    if (!btn) return;
    const now = Date.now();
    if (now - lastEmoteSent < 1500) { showToast('Easy there — one emote every moment.', false); return; }
    lastEmoteSent = now;
    SFX.click();
    send({ type: 'emote', emote: btn.dataset.emote });
    btn.classList.add('cooling');
    setTimeout(() => btn.classList.remove('cooling'), 1500);
  });
}

// Resign: two-step confirm so nobody throws a match by accident.
let resignArmed = false;
let resignTimer = null;
function resetResignButton() {
  resignArmed = false;
  clearTimeout(resignTimer);
  els.resignBtn.textContent = 'Resign';
  els.resignBtn.classList.remove('confirm');
}
els.resignBtn.addEventListener('click', () => {
  SFX.click();
  if (!resignArmed) {
    resignArmed = true;
    els.resignBtn.textContent = 'Confirm resign?';
    els.resignBtn.classList.add('confirm');
    clearTimeout(resignTimer);
    resignTimer = setTimeout(resetResignButton, 3000);
    return;
  }
  resetResignButton();
  send({ type: 'resign' });
});

// Coupon redeem form + in-match ability buttons
if (els.couponForm) {
  els.couponForm.addEventListener('submit', onRedeemCoupon);
}
for (const effect of ABILITY_EFFECTS) {
  const btn = abilityButtonFor(effect);
  if (btn) btn.addEventListener('click', () => { SFX.unlock(); selectAbility(effect); });
}

$('btn-copy').addEventListener('click', () => copyCode());
$('btn-copy-2').addEventListener('click', () => copyCode());
function copyCode() {
  if (state.code) {
    navigator.clipboard?.writeText(state.code);
    SFX.click();
    showToast('Room code copied', true);
  }
}

els.muteBtn.addEventListener('click', () => {
  const muted = SFX.toggleMute();
  els.muteBtn.textContent = muted ? '🔇' : '🔊';
  if (!muted) SFX.click();
});

if (els.btnReady) {
  els.btnReady.addEventListener('click', () => {
    SFX.unlock();
    SFX.click();
    send({ type: 'ready' });
  });
}

cv.addEventListener('click', (ev) => {
  const lane = laneFromEvent(ev);
  if (lane === null || !state.selection) return;
  SFX.unlock();
  if (state.selection.kind === 'unit') {
    SFX.deploy();
    send({ type: 'deploy', unit: state.selection.unit, lane });
  } else if (state.selection.kind === 'turret') {
    SFX.build();
    send({ type: 'turret', lane });
  } else if (state.selection.kind === 'wall') {
    SFX.build();
    const hasWall = (state.snapNow?.walls || []).some((w) => w.side === state.seat && w.lane === lane);
    send({ type: hasWall ? 'upgradeWall' : 'wall', lane });
  } else if (state.selection.kind === 'scout') {
    SFX.click();
    send({ type: 'scout', lane });
  } else if (state.selection.kind === 'ability') {
    SFX.click();
    send({ type: 'fieldEffect', effect: state.selection.effect, lane });
  }
});
cv.addEventListener('mousemove', (ev) => { state.hoverLane = laneFromEvent(ev); });
cv.addEventListener('mouseleave', () => { state.hoverLane = null; });
cv.addEventListener('contextmenu', (ev) => {
  ev.preventDefault();
  if (state.selection) {
    clearSelection();
    return;
  }
  const lane = laneFromEvent(ev);
  if (lane !== null && state.snapNow && state.snapNow.phase === 'prep') {
    SFX.click();
    send({ type: 'recallUnit', lane });
  }
});

// Compact-mode chrome: drawer open/close, rotate-device dismiss
els.shopToggle.addEventListener('click', () => { SFX.click(); setSidebar(); });
els.sidebarClose.addEventListener('click', () => { SFX.click(); setSidebar(false); });
els.sidebarBackdrop.addEventListener('click', () => setSidebar(false));
if (els.rotateDismiss) els.rotateDismiss.addEventListener('click', () => {
  document.getElementById('rotate-overlay').classList.add('dismissed');
});

window.addEventListener('keydown', (ev) => {
  if (state.screen !== 'game') return;
  if (ev.key === 'Escape') { clearSelection(); setSidebar(false); return; }
  if (ev.code === 'Space' && !ev.repeat && document.activeElement?.tagName !== 'INPUT') {
    ev.preventDefault();
    if (state.snapNow && state.snapNow.phase === 'prep') { SFX.click(); send({ type: 'ready' }); }
    return;
  }
  if (state.coupon && COUPON_HOTKEYS[ev.key]) {
    SFX.unlock();
    const couponKey = COUPON_HOTKEYS[ev.key];
    if (couponKey === 'dragon' || couponKey === 'valkyrie') { SFX.click(); selectUnit(couponKey); } else { selectAbility(couponKey); }
    return;
  }
  const unit = HOTKEYS[ev.key];
  if (!unit) return;
  SFX.unlock();
  if (unit === 'factory') { send({ type: state.snapNow?.players[state.seat].factory ? 'upgradeFactory' : 'factory' }); return; }
  if (unit === 'turret') { selectTurret(); return; }
  if (unit === 'wall') { selectWall(); return; }
  if (unit === 'scout') { selectScout(); return; }
  SFX.click();
  selectUnit(unit);
});

// Browsers require a user gesture before audio can start.
document.addEventListener('pointerdown', () => SFX.unlock(), { once: true });

// boot
els.muteBtn.textContent = SFX.isMuted() ? '🔇' : '🔊';
refreshCouponStatus();
setInterval(refreshCouponStatus, 60000);
document.addEventListener('visibilitychange', () => { if (!document.hidden) refreshCouponStatus(); });
requestAnimationFrame(draw);
tryResume();
