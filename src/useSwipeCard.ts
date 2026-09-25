import { useEffect, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent } from "react";
import { emitHaptic, reducedMotion } from "./motion";

type Phase = "rest" | "drag" | "exit" | "enter";
type Drag = { id: number; x0: number; y0: number; active: boolean; step: number; armed: -1 | 0 | 1 };

export function useSwipeCard(key: string, onRight: () => Promise<boolean> | boolean | void, onLeft: () => void, allowRight = true, locked = false) {
  const [x, setX] = useState(0);
  const [phase, setPhase] = useState<Phase>("rest");
  const drag = useRef<Drag | null>(null);
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
  const confirm = (force = false) => {
    if ((!allowRight && !force) || locked || phase === "exit") { snap(); return; }
    emitHaptic("success");
    setPhase("exit");
    setX(520);
    const committingKey = previous.current;
    timer.current = window.setTimeout(async () => {
      try {
        const success = await onRight();
        if (success === false) snap();
        else timer.current = window.setTimeout(() => { if (previous.current === committingKey) snap(); }, 900);
      } catch { snap(); }
    }, reducedMotion() ? 0 : 230);
  };
  const down = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (locked || phase === "exit" || (event.target as HTMLElement).closest("button,input,select,textarea,a,summary")) return;
    drag.current = { id: event.pointerId, x0: event.clientX, y0: event.clientY, active: false, step: 0, armed: 0 };
  };
  const move = (event: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || d.id !== event.pointerId) return;
    const dx = event.clientX - d.x0;
    const dy = event.clientY - d.y0;
    if (!d.active) {
      if (Math.abs(dx) < 6 && Math.abs(dy) < 6) return;
      if (Math.abs(dx) <= Math.abs(dy)) { drag.current = null; return; }
      d.active = true;
      event.currentTarget.setPointerCapture(event.pointerId);
      setPhase("drag");
      emitHaptic("grab", { x: event.clientX, y: event.clientY });
    }
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
    if (distance > 90 && allowRight) confirm();
    else if (distance < -90) { emitHaptic("sheet"); snap(); onLeft(); }
    else { emitHaptic("unarm"); snap(); }
  };
  const cancel = () => { drag.current = null; snap(); };
  const progress = Math.min(1, Math.abs(x) / 160);
  return {
    phase, x, progress, confirm,
    handlers: { onPointerDown: down, onPointerMove: move, onPointerUp: up, onPointerCancel: cancel },
    style: { "--swipe-x": `${x}px`, "--swipe-rotation": `${x / 24}deg`, "--swipe-progress": progress } as CSSProperties,
  };
}
