import { useEffect } from "react";

export function useSheetFocus(open: boolean) {
  useEffect(() => {
    if (!open) return;
    const before = document.activeElement as HTMLElement | null;
    const dialog = document.querySelector<HTMLElement>('[role="dialog"][aria-modal="true"]');
    const selector = 'button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),[tabindex="0"]';
    dialog?.querySelector<HTMLElement>("input,select,button")?.focus();
    const trap = (event: KeyboardEvent) => {
      if (event.key !== "Tab") return;
      const items = Array.from(dialog?.querySelectorAll<HTMLElement>(selector) || []);
      if (!items.length) return;
      const first = items[0], last = items.at(-1)!;
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
    };
    document.addEventListener("keydown", trap);
    return () => { document.removeEventListener("keydown", trap); before?.focus(); };
  }, [open]);
}
