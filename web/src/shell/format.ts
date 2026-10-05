// Display formatting only. All budget math happens on the server (ADR-0003).

const whole = new Intl.NumberFormat("en-US", { maximumFractionDigits: 0 });
const fine = new Intl.NumberFormat("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });

/** Cents to "$1,234" (rounded to the dollar), with a minus sign when negative. */
export function dollars(cents: number): string {
  const d = Math.round(cents / 100);
  return (d < 0 ? "-$" : "$") + whole.format(Math.abs(d));
}

/** Cents to "$1,234.56". */
export function dollarsAndCents(cents: number): string {
  return (cents < 0 ? "-$" : "$") + fine.format(Math.abs(cents) / 100);
}
