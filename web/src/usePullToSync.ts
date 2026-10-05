import { useEffect, useRef, useState } from "react";
import { emitHaptic } from "./motion";

export function usePullToSync(onSync?: () => void | Promise<void>, syncing = false) {
  const root = useRef<HTMLDivElement>(null);
  const callback = useRef(onSync);
  callback.current = onSync;
  const [pull, setPull] = useState(0);
  const [active, setActive] = useState(false);
  const start = useRef<number | null>(null);
  const current = useRef(0);
  const activeRef = useRef(false);
  const step = useRef(0);
  const armed = useRef(false);
  useEffect(() => {
    const element = root.current;
    if (!element) return;
    const canStart = (target: EventTarget | null) =>
      !syncing && window.scrollY <= 2 && target instanceof Element && !target.closest("button,input,select,textarea,a");
    const begin = (y: number) => {
      start.current = y;
      step.current = 0;
      armed.current = false;
    };
    const movePull = (y: number, preventDefault: () => void) => {
      if (start.current === null) return false;
      const delta = y - start.current;
      if (delta <= 8 && !activeRef.current) return false;
      if (delta < 0) { start.current = null; return false; }
      preventDefault();
      const distance = Math.max(0, delta) * .45;
      current.current = distance;
      setPull(distance);
      activeRef.current = true;
      setActive(true);
      const nextStep = Math.min(3, Math.floor(distance / 18));
      if (distance <= 64 && nextStep > step.current) emitHaptic("tension", undefined, .4 + nextStep * .2);
      step.current = nextStep;
      if (distance > 64 && !armed.current) { armed.current = true; emitHaptic("threshold"); }
      else if (distance <= 64 && armed.current) { armed.current = false; emitHaptic("unarm"); }
      return true;
    };
    const finish = () => {
      if (start.current === null) return;
      start.current = null;
      if (!activeRef.current) return;
      activeRef.current = false;
      if (current.current > 64) {
        setPull(56);
        Promise.resolve(callback.current?.()).finally(() => window.setTimeout(() => { setPull(0); setActive(false); }, 500));
      } else { setPull(0); setActive(false); emitHaptic("unarm"); }
      current.current = 0;
    };
    const down = (event: TouchEvent) => {
      if (!canStart(event.target)) return;
      const y = event.touches[0]?.clientY;
      if (y !== undefined) begin(y);
    };
    const move = (event: TouchEvent) => {
      if (event.touches.length) movePull(event.touches[0].clientY, () => event.preventDefault());
    };
    let pointerId: number | null = null;
    const pointerDown = (event: PointerEvent) => {
      if (event.pointerType === "touch" || event.button !== 0 || !canStart(event.target)) return;
      event.preventDefault();
      pointerId = event.pointerId;
      begin(event.clientY);
    };
    const pointerMove = (event: PointerEvent) => {
      if (event.pointerId !== pointerId) return;
      if (movePull(event.clientY, () => event.preventDefault())) element.setPointerCapture?.(event.pointerId);
    };
    const pointerUp = (event: PointerEvent) => {
      if (event.pointerId !== pointerId) return;
      pointerId = null;
      finish();
    };
    element.addEventListener("touchstart", down, { passive: true });
    element.addEventListener("touchmove", move, { passive: false });
    element.addEventListener("touchend", finish);
    element.addEventListener("touchcancel", finish);
    element.addEventListener("pointerdown", pointerDown);
    element.addEventListener("pointermove", pointerMove);
    element.addEventListener("pointerup", pointerUp);
    element.addEventListener("pointercancel", pointerUp);
    return () => {
      element.removeEventListener("touchstart", down);
      element.removeEventListener("touchmove", move);
      element.removeEventListener("touchend", finish);
      element.removeEventListener("touchcancel", finish);
      element.removeEventListener("pointerdown", pointerDown);
      element.removeEventListener("pointermove", pointerMove);
      element.removeEventListener("pointerup", pointerUp);
      element.removeEventListener("pointercancel", pointerUp);
    };
  }, [syncing]);
  return { root, pull, active };
}
