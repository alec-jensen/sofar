import { useEffect, useRef, useState } from "react";
import { reducedMotion } from "./motion";

export default function RollingNumber({ value, format, className }: { value: number; format: (value: number) => string; className?: string }) {
  const [display, setDisplay] = useState(() => reducedMotion() ? value : 0);
  const current = useRef(display);
  useEffect(() => {
    if (reducedMotion() || !Number.isFinite(value)) {
      current.current = value;
      setDisplay(value);
      return;
    }
    const start = current.current;
    const change = value - start;
    const began = performance.now();
    let frame = 0;
    const step = (now: number) => {
      const t = Math.max(0, (now - began) / 1000);
      const progress = 1 - Math.exp(-15 * t) * (1 + 15 * t);
      const next = progress > .9995 ? value : start + change * progress;
      current.current = next;
      setDisplay(next);
      if (progress <= .9995) frame = requestAnimationFrame(step);
    };
    frame = requestAnimationFrame(step);
    return () => cancelAnimationFrame(frame);
  }, [value]);
  return <span className={className}>{format(display)}</span>;
}
