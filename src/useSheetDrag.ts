import { useEffect, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent } from "react";
import { emitHaptic, reducedMotion } from "./motion";

type Drag = { id: number; start: number; previous: number; at: number; velocity: number; active: boolean; step: number; armed: boolean };

export function useSheetDrag(open: boolean, onClose: () => void) {
  const [dy, setDy] = useState(0);
  const [dragging, setDragging] = useState(false);
  const [closing, setClosing] = useState(false);
  const drag = useRef<Drag | null>(null);
  const currentY = useRef(0);
  const timer = useRef<number | null>(null);
  useEffect(() => {
    if (open) { setDy(0); currentY.current = 0; setDragging(false); setClosing(false); }
    return () => { if (timer.current !== null) clearTimeout(timer.current); };
  }, [open]);
  const close = () => {
    if (closing) return;
    emitHaptic("sheetClose");
    setClosing(true);
    setDragging(false);
    timer.current = window.setTimeout(onClose, reducedMotion() ? 0 : 260);
  };
  const down = (event: ReactPointerEvent<HTMLDivElement>) => {
    if ((event.target as HTMLElement).closest("button,input,select,textarea,a,summary") || event.currentTarget.scrollTop > 0) return;
    drag.current = { id: event.pointerId, start: event.clientY, previous: event.clientY, at: performance.now(), velocity: 0, active: false, step: 0, armed: false };
  };
  const move = (event: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || d.id !== event.pointerId) return;
    const delta = event.clientY - d.start;
    if (!d.active) {
      if (Math.abs(delta) <= 6) return;
      if (delta < 0) { drag.current = null; return; }
      d.active = true;
      event.currentTarget.setPointerCapture(event.pointerId);
      setDragging(true);
      emitHaptic("grab", { x: event.clientX, y: event.clientY });
    }
    const now = performance.now();
    d.velocity = (event.clientY - d.previous) / Math.max(1, now - d.at);
    d.previous = event.clientY;
    d.at = now;
    const y = delta < 0 ? delta * .2 : delta;
    const step = Math.min(3, Math.floor(y / 30));
    if (!d.armed && step > d.step) emitHaptic("tension", { x: event.clientX, y: event.clientY }, .4 + step * .2);
    if (!d.armed && y > 120) { d.armed = true; emitHaptic("threshold", { x: event.clientX, y: event.clientY }); }
    else if (d.armed && y <= 120) { d.armed = false; emitHaptic("unarm"); }
    d.step = step;
    currentY.current = y;
    setDy(y);
  };
  const up = (event: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || d.id !== event.pointerId) return;
    drag.current = null;
    setDragging(false);
    if (!d.active) return;
    if (currentY.current > 120 || d.velocity > .6) close();
    else { emitHaptic("unarm"); currentY.current = 0; setDy(0); }
  };
  const cancel = () => { drag.current = null; setDragging(false); currentY.current = 0; setDy(0); };
  return {
    close, closing, dragging,
    handlers: { onPointerDown: down, onPointerMove: move, onPointerUp: up, onPointerCancel: cancel },
    style: { "--sheet-y": closing ? "110vh" : `${dy}px` } as CSSProperties,
  };
}
