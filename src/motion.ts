export type MotionMode = "expressive" | "off";
export type HapticName = "key" | "tick" | "tension" | "grab" | "tap" | "press" | "sheet" | "sheetClose" | "threshold" | "unarm" | "toggle" | "toggleOff" | "success" | "soft" | "thud" | "error";
type Pulse = [gap: number, duration: number, strength: number];

const patterns: Record<HapticName, { pulses: Pulse[]; sharp: number }> = {
  key: { pulses: [[0, 4, .25]], sharp: .95 },
  tick: { pulses: [[0, 6, .4]], sharp: .85 },
  tension: { pulses: [[0, 6, .3]], sharp: .7 },
  grab: { pulses: [[0, 6, .3], [10, 10, .6]], sharp: .6 },
  tap: { pulses: [[0, 10, .5]], sharp: .7 },
  press: { pulses: [[0, 16, .75]], sharp: .55 },
  sheet: { pulses: [[0, 12, .2], [0, 14, .45], [0, 18, .7]], sharp: .35 },
  sheetClose: { pulses: [[0, 16, .55], [0, 12, .25]], sharp: .35 },
  threshold: { pulses: [[0, 12, 1]], sharp: 1 },
  unarm: { pulses: [[0, 6, .3]], sharp: .5 },
  toggle: { pulses: [[0, 10, .4], [40, 14, .85]], sharp: .7 },
  toggleOff: { pulses: [[0, 14, .75], [40, 8, .3]], sharp: .6 },
  success: { pulses: [[0, 14, .55], [70, 24, 1]], sharp: .6 },
  soft: { pulses: [[0, 22, .65], [110, 14, .4]], sharp: .3 },
  thud: { pulses: [[0, 30, 1], [0, 24, .45], [0, 16, .2]], sharp: .15 },
  error: { pulses: [[0, 18, .85], [45, 18, .85], [45, 18, .85]], sharp: .8 },
};

let motionMode: MotionMode = "expressive";
let hapticsEnabled = true;
let fallbackSwitch: HTMLElement | null = null;
let audioContext: AudioContext | null = null;

export const reducedMotion = () => motionMode === "off" || window.matchMedia("(prefers-reduced-motion: reduce)").matches;

function spring(stiffness: number, damping: number) {
  const w = Math.sqrt(stiffness);
  const wd = w * Math.sqrt(Math.max(1e-6, 1 - damping * damping));
  const seconds = damping < 1 ? Math.log(1000) / (damping * w) : 9.2 / w;
  const points: number[] = [];
  for (let i = 0; i <= 44; i++) {
    const t = seconds * i / 44;
    const x = damping < 1
      ? 1 - Math.exp(-damping * w * t) * (Math.cos(wd * t) + damping * w / wd * Math.sin(wd * t))
      : 1 - Math.exp(-w * t) * (1 + w * t);
    points.push(i === 44 ? 1 : Number(x.toFixed(4)));
  }
  const linear = `linear(${points.join(",")})`;
  return { duration: Math.round(seconds * 1000), easing: CSS.supports("animation-timing-function", linear) ? linear : "cubic-bezier(.2,.8,.2,1)" };
}

export function configureMotion(mode: MotionMode) {
  motionMode = mode;
  const tokens = {
    fast: spring(800, .6),
    def: spring(380, .72),
    slow: spring(200, .8),
    fx: spring(1600, 1),
  };
  const root = document.documentElement;
  root.dataset.motion = reducedMotion() ? "off" : mode;
  for (const [name, token] of Object.entries(tokens)) {
    root.style.setProperty(`--sp-${name}`, reducedMotion() ? "ease" : token.easing);
    root.style.setProperty(`--d-${name}`, `${reducedMotion() ? name === "fx" ? 150 : 1 : token.duration}ms`);
  }
  return tokens;
}

export function playPageEntrance(page: string) {
  if (reducedMotion()) return;
  const selectors: Record<string, string> = {
    Overview: ".reference-home-top,.reference-hero-label,.reference-hero-number,.reference-hero-sub,.reference-review,.reference-goal,.reference-upcoming,.reference-income,.reference-buy-link",
    "Review inbox": ".review-progress,.review-progress-track,.review-card,.review-aside",
    Transactions: ".transaction-page .table-toolbar,.history-chips,.history-filter-buttons,.history-summary,.history-day",
    Calculators: ".calc-buy-link,.calc-section-label,.calc-accordion,.calc-saved-list,.calc-setup-links",
    Categories: ".design-setup-heading,.design-setup-intro,.design-category-group",
    "Sorting rules": ".design-setup-heading,.design-setup-intro,.design-rule,.design-rules-fallback",
    "Should I buy this": ".design-buy-breadcrumb,.design-buy-entry,.design-buy-answer,.design-buy-impact,.design-buy-invest",
  };
  const scope = document.querySelector("main");
  const items = Array.from(scope?.querySelectorAll<HTMLElement>(selectors[page] || ".page-heading,.card,.summary-strip") || []);
  const spatial = spring(200, .8);
  const effects = spring(1600, 1);
  items.slice(0, 16).forEach((element, index) => {
    const delay = 80 + index * 55;
    element.animate([{ transform: "translateY(28px) scale(.97)" }, { transform: "none" }], { duration: spatial.duration, delay, easing: spatial.easing, fill: "backwards" });
    element.animate([{ opacity: 0 }, { opacity: 1 }], { duration: effects.duration + 120, delay, easing: effects.easing, fill: "backwards" });
  });
  scope?.querySelectorAll<HTMLElement>(".reference-progress i,.review-progress-track span,.design-buy-track span,.design-category-progress i").forEach((element, index) => {
    element.animate([{ transform: "scaleX(0)" }, { transform: "scaleX(1)" }], { duration: spatial.duration + 200, delay: 260 + index * 55, easing: spatial.easing, fill: "backwards" });
  });
}

function vibratePattern(pulses: Pulse[]) {
  const events: [0 | 1, number][] = [];
  for (const [gap, duration, strength] of pulses) {
    if (gap) events.push([0, gap]);
    if (strength >= .95) events.push([1, duration]);
    else if (duration < 14) {
      const on = Math.max(2, Math.round(duration * (.35 + .65 * strength)));
      events.push([1, on]);
      if (duration > on) events.push([0, duration - on]);
    } else {
      const count = Math.max(1, Math.round(duration / 12));
      for (let i = 0; i < count; i++) {
        const on = Math.max(2, Math.round(12 * strength));
        events.push([1, on]);
        if (12 > on) events.push([0, 12 - on]);
      }
    }
  }
  const merged: [0 | 1, number][] = [];
  for (const event of events) {
    const last = merged.at(-1);
    if (last?.[0] === event[0]) last[1] += event[1];
    else merged.push([...event]);
  }
  while (merged[0]?.[0] === 0) merged.shift();
  if (merged.at(-1)?.[0] === 0) merged.pop();
  return merged.map(event => event[1]);
}

function ripple(x: number, y: number, pulses: Pulse[]) {
  if (reducedMotion()) return;
  let elapsed = 0;
  for (const [gap, duration, strength] of pulses) {
    elapsed += gap;
    const delay = elapsed;
    elapsed += duration;
    if (strength < .12) continue;
    const size = 20 + 16 * strength;
    const element = document.createElement("span");
    element.setAttribute("data-feedback-ripple", "");
    element.style.cssText = `position:fixed;left:${x - size / 2}px;top:${y - size / 2}px;width:${size}px;height:${size}px;border-radius:50%;border:${(1 + 2 * strength).toFixed(1)}px solid #8fae9a;box-sizing:border-box;pointer-events:none;z-index:99999;opacity:0`;
    document.body.appendChild(element);
    element.animate([{ transform: "scale(.35)", opacity: .35 + .6 * strength }, { transform: `scale(${1.3 + 1.5 * strength})`, opacity: 0 }], { duration: 300 + 260 * strength, delay, easing: "cubic-bezier(.2,.8,.2,1)", fill: "backwards" }).onfinish = () => element.remove();
  }
}

function sound(pulses: Pulse[], sharp: number) {
  try {
    audioContext ||= new AudioContext();
    if (audioContext.state === "suspended") void audioContext.resume();
    let at = audioContext.currentTime + .005;
    for (const [gap, duration, strength] of pulses) {
      at += gap / 1000;
      const oscillator = audioContext.createOscillator();
      const gain = audioContext.createGain();
      const length = duration / 1000 * 1.6 + .012;
      oscillator.type = "sine";
      oscillator.frequency.value = 110 + 280 * sharp + 60 * strength;
      gain.gain.setValueAtTime(0, at);
      gain.gain.linearRampToValueAtTime(.16 * strength + .01, at + .002);
      gain.gain.exponentialRampToValueAtTime(.0001, at + length);
      oscillator.connect(gain).connect(audioContext.destination);
      oscillator.start(at);
      oscillator.stop(at + length + .02);
      at += duration / 1000;
    }
  } catch { /* audio feedback is optional */ }
}

export function setHapticEnabled(enabled: boolean) { hapticsEnabled = enabled; }

export function emitHaptic(name: HapticName, point?: { x: number; y: number }, strength = 1) {
  if (!hapticsEnabled) return;
  const pattern = patterns[name];
  const pulses = pattern.pulses.map(([gap, duration, intensity]) => [gap, duration, Math.min(1, intensity * strength)] as Pulse);
  if (typeof navigator.vibrate === "function") {
    try { navigator.vibrate(vibratePattern(pulses)); } catch { /* unsupported device */ }
  } else if (fallbackSwitch) {
    let elapsed = 0;
    let last = -99;
    for (const [gap, duration, intensity] of pulses) {
      elapsed += gap;
      if (intensity >= .3 && elapsed - last >= 30) {
        const at = elapsed;
        last = at;
        if (at) window.setTimeout(() => fallbackSwitch?.click(), at);
        else fallbackSwitch.click();
      }
      elapsed += duration;
    }
  }
  if (point) ripple(point.x, point.y, pulses);
  if (!navigator.vibrate && !fallbackSwitch && point) sound(pulses, pattern.sharp);
}

export function installFeedback() {
  if (!navigator.vibrate && /iPhone|iPad|iPod/.test(navigator.userAgent)) {
    try {
      const label = document.createElement("label");
      const input = document.createElement("input");
      input.type = "checkbox";
      input.setAttribute("switch", "");
      label.appendChild(input);
      label.style.cssText = "position:fixed;left:-200px;top:0;opacity:0;pointer-events:none";
      document.body.appendChild(label);
      fallbackSwitch = label;
    } catch { /* iOS fallback unavailable */ }
  }
  const onPointerDown = (event: PointerEvent) => {
    const target = event.target as HTMLElement;
    const hit = target.closest<HTMLElement>("button,a,summary,input,select,[role='switch'],[data-h]");
    if (!hit || hit.matches(":disabled") || hit.closest("[data-feedback-ripple]")) return;
    if (hit.closest<HTMLElement>("[data-h]")?.dataset.h === "manual") return;
    const explicit = hit.closest<HTMLElement>("[data-h]")?.dataset.h as HapticName | undefined;
    const name = explicit && patterns[explicit] ? explicit : hit.matches("input[type='range']") ? "tick" : hit.matches("input,select") ? "key" : hit.matches("[role='switch']") ? "toggle" : hit.matches("button") && getComputedStyle(hit).borderTopLeftRadius.includes("px") ? "press" : "tap";
    emitHaptic(name, { x: event.clientX, y: event.clientY });
  };
  const lastValues = new WeakMap<HTMLInputElement, string>();
  const onInput = (event: Event) => {
    const input = event.target;
    if (!(input instanceof HTMLInputElement)) return;
    if (input.type === "range") {
      if (lastValues.get(input) !== input.value) {
        lastValues.set(input, input.value);
        emitHaptic(input.value === input.min || input.value === input.max ? "threshold" : "tick");
      }
    } else if (input.type === "text" || input.type === "number" || input.type === "search") emitHaptic("key");
  };
  document.addEventListener("pointerdown", onPointerDown, true);
  document.addEventListener("input", onInput, true);
  return () => {
    document.removeEventListener("pointerdown", onPointerDown, true);
    document.removeEventListener("input", onInput, true);
    fallbackSwitch?.remove();
    fallbackSwitch = null;
  };
}
