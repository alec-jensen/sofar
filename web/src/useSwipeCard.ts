import { useEffect, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent } from "react";
import { emitHaptic, reducedMotion } from "./motion";

type Phase = "rest" | "drag" | "exit" | "enter";
type Drag = { id: number; x0: number; y0: number; active: boolean; step: number; armed: -1 | 0 | 1 };

const COMMIT_DISTANCE = 90; // a slow, deliberate drag commits past this many pixels
const INERTIA_MS = 180; // how far a flick would coast, measured in milliseconds of release speed
const FLICK_TRAVEL = 24; // a flick still has to move at least this far, so a twitch isn't a swipe
const FLICK_REACH = 140; // ...and its projected landing spot has to get at least this far

/**
 * What a released drag should do. Slow drags commit by distance; quick flicks
 * commit by where their momentum would carry them, so a short, fast thumb flick
 * works as well as a long drag. `velocity` is px/ms, positive to the right.
 */
export function decideRelease(dx: number, velocity: number, allowRight = true): "confirm" | "change" | "snap" {
  const projected = dx + velocity * INERTIA_MS;
  const flicked = Math.abs(dx) > FLICK_TRAVEL && Math.abs(projected) > FLICK_REACH && Math.sign(velocity) === Math.sign(dx);
  if (dx > 0 && (dx > COMMIT_DISTANCE || flicked)) return allowRight ? "confirm" : "snap";
  if (dx < 0 && (dx < -COMMIT_DISTANCE || flicked)) return "change";
  return "snap";
}

type Sample = { x: number; t: number };
/** Release speed in px/ms from the last ~100ms of movement; zero if the finger had come to rest. */
export function releaseVelocity(samples: Sample[], now: number) {
  if (samples.length < 2) return 0;
  const first = samples[0];
  const last = samples[samples.length - 1];
  if (now - last.t > 80) return 0;
  const dt = last.t - first.t;
  return dt > 4 ? (last.x - first.x) / dt : 0;
}

export function useSwipeCard(key: string, onRight: () => Promise<boolean> | boolean | void, onLeft: () => void, allowRight = true, locked = false) {
  const [x, setX] = useState(0);
  const [phase, setPhase] = useState<Phase>("rest");
  const [exitMs, setExitMs] = useState(230);
  const drag = useRef<Drag | null>(null);
  const samples = useRef<Sample[]>([]);
  const timer = useRef<number | null>(null);
  const previous = useRef(key);
  useEffect(() => {
    if (previous.current === key) return;
    previous.current = key;
    drag.current = null;
    setX(0);
    if (reducedMotion()) { setPhase("rest"); return; }
    setPhase("enter");
    const frame = requestAnimationFrame(() => requestAnimationFrame(() => setPhase("rest")));
    return () => cancelAnimationFrame(frame);
  }, [key]);
  useEffect(() => () => { if (timer.current !== null) clearTimeout(timer.current); }, []);

  const snap = () => { setX(0); setPhase("rest"); };
  const confirm = (force = false, velocity = 0) => {
    if ((!allowRight && !force) || locked || phase === "exit") { snap(); return; }
    emitHaptic("success");
    // The harder it was thrown, the faster it leaves.
    const ms = reducedMotion() ? 0 : Math.round(Math.min(260, Math.max(130, 250 - Math.max(0, velocity) * 110)));
    setExitMs(ms);
    setPhase("exit");
    setX(520);
    const committingKey = previous.current;
    timer.current = window.setTimeout(async () => {
      try {
        const success = await onRight();
        if (success === false) snap();
        else timer.current = window.setTimeout(() => { if (previous.current === committingKey) snap(); }, 900);
      } catch { snap(); }
    }, ms);
  };
  const down = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (locked || phase === "exit" || (event.target as HTMLElement).closest("button,input,select,textarea,a,summary")) return;
    drag.current = { id: event.pointerId, x0: event.clientX, y0: event.clientY, active: false, step: 0, armed: 0 };
    samples.current = [{ x: event.clientX, t: event.timeStamp }];
  };
  const move = (event: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || d.id !== event.pointerId) return;
    const dx = event.clientX - d.x0;
    const dy = event.clientY - d.y0;
    if (!d.active) {
      if (Math.abs(dx) < 6 && Math.abs(dy) < 6) return;
      // A thumb rarely travels perfectly flat, so favor a horizontal read over a strictly equal one.
      if (Math.abs(dx) < Math.abs(dy) * 0.8) { drag.current = null; return; }
      d.active = true;
      try { event.currentTarget.setPointerCapture(event.pointerId); } catch { /* the gesture still works without capture */ }
      setPhase("drag");
      emitHaptic("grab", { x: event.clientX, y: event.clientY });
    }
    const trail = samples.current;
    trail.push({ x: event.clientX, t: event.timeStamp });
    while (trail.length > 1 && event.timeStamp - trail[0].t > 100) trail.shift();
    const step = Math.min(3, Math.floor(Math.abs(dx) / 30));
    const armed: -1 | 0 | 1 = dx > 90 ? 1 : dx < -90 ? -1 : 0;
    if (!armed && step > d.step) emitHaptic("tension", { x: event.clientX, y: event.clientY }, .4 + step * .2);
    if (armed && armed !== d.armed) emitHaptic("threshold", { x: event.clientX, y: event.clientY });
    else if (!armed && d.armed) emitHaptic("unarm");
    d.step = step;
    d.armed = armed;
    setX(dx);
  };
  const up = (event: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || d.id !== event.pointerId) return;
    drag.current = null;
    if (!d.active) return;
    const distance = event.clientX - d.x0;
    const velocity = releaseVelocity(samples.current, event.timeStamp);
    const outcome = decideRelease(distance, velocity, allowRight);
    if (outcome === "confirm") confirm(false, velocity);
    else if (outcome === "change") { emitHaptic("sheet"); snap(); onLeft(); }
    else { emitHaptic("unarm"); snap(); }
  };
  const cancel = () => { drag.current = null; snap(); };
  const progress = Math.min(1, Math.abs(x) / 160);
  return {
    phase, x, progress, confirm,
    handlers: { onPointerDown: down, onPointerMove: move, onPointerUp: up, onPointerCancel: cancel },
    style: { "--swipe-x": `${x}px`, "--swipe-rotation": `${x / 24}deg`, "--swipe-progress": progress, "--swipe-exit-ms": `${exitMs}ms` } as CSSProperties,
  };
}
