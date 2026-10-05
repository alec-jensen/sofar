import { useEffect, useRef, useState } from "react";
import { ArrowLeft } from "lucide-react";
import RollingNumber from "./RollingNumber";
import { emitHaptic, reducedMotion } from "./motion";
import { budget, money, type State } from "./model";
import { spendingGuide } from "./Dashboard";
import { investmentProjection } from "./purchaseMath";

const decimal = (value: number) => new Intl.NumberFormat("en-US", { maximumFractionDigits: 1 }).format(value);

export default function ShouldIBuy({ state, onExplore, onDone, onSleep, initial }: { state: State; onExplore?: () => void; onDone?: () => void; onSleep?: (item: string, price: number) => void; initial?: { item: string; price: number } }) {
  const [item, setItem] = useState(initial?.item ?? "");
  const [priceText, setPriceText] = useState(initial ? String(initial.price / 100) : "");
  const [rate, setRate] = useState(7);
  const n = Number(priceText);
  const price = priceText.trim() && Number.isFinite(n) && n >= .01 && n <= 1_000_000 ? Math.round(n * 100) : null;
  const guide = spendingGuide(state);
  const b = budget(state);
  const days = price !== null && guide.perDay > 0 ? price / guide.perDay : null;
  const share = price !== null && guide.left > 0 ? price / guide.left * 100 : null;
  const after = price !== null ? guide.left - price : null;
  const perDayAfter = after !== null ? Math.round(after / guide.daysLeft) : null;
  const quick = [25, 80, 180, 600];
  const answer = useRef<HTMLElement>(null);
  const previousBucket = useRef<number | null>(null);
  const bucket = price === null ? 0 : days !== null && days < 1 ? 1 : after !== null && after >= 0 ? 2 : 3;
  useEffect(() => {
    if (previousBucket.current !== null && previousBucket.current !== bucket && !reducedMotion()) {
      const easing = getComputedStyle(document.documentElement).getPropertyValue("--sp-fast").trim() || "cubic-bezier(.2,.8,.2,1)";
      answer.current?.animate([{ transform: "scale(.9)", borderRadius: "34px" }, { transform: "none", borderRadius: "20px" }], { duration: 420, easing });
    }
    previousBucket.current = bucket;
  }, [bucket]);
  return <div className="design-buy">
    <div className="design-buy-breadcrumb"><button onClick={onExplore}><ArrowLeft size={16} /> tools</button><span>/</span><h1>should i buy this?</h1></div>
    <div className="design-buy-grid">
      <div className="design-buy-main">
        <section className="design-buy-entry">
          <label htmlFor="design-buy-item" className="sr-only">what are you buying?</label>
          <input id="design-buy-item" className="design-buy-item" value={item} maxLength={60} onChange={event => setItem(event.target.value)} placeholder="what are you thinking of buying?" />
          <label htmlFor="design-buy-price" className="sr-only">price in dollars</label>
          <div className="design-buy-price"><span aria-hidden="true">$</span><input id="design-buy-price" type="number" min="0.01" max="1000000" step="0.01" inputMode="decimal" value={priceText} onChange={event => setPriceText(event.target.value)} onBlur={event => { if (priceText && price === null) { emitHaptic("error"); event.currentTarget.animate([{ transform: "translateX(0)" }, { transform: "translateX(-7px)" }, { transform: "translateX(5px)" }, { transform: "translateX(-3px)" }, { transform: "none" }], { duration: 360, easing: "ease-out" }); } }} placeholder="0" /></div>
          <div className="design-buy-quick" role="group" aria-label="quick prices">{quick.map(value => <button key={value} type="button" data-h="tick" className={Number(priceText) === value ? "active" : ""} onClick={() => setPriceText(String(value))}>${value}</button>)}</div>
          {priceText && price === null && <p className="design-buy-error" role="alert">enter a price between $0.01 and $1,000,000.</p>}
        </section>
        <section className="design-buy-answer" ref={answer} aria-live="polite">
          <span>that’s</span>
          <strong>{days === null ? "—" : <><RollingNumber value={days} format={value => decimal(value)} /> days</>}</strong>
          <span>of spending, at {money(guide.perDay)}/day</span>
        </section>
        <section className="design-buy-impact">
          <div><span>of your {money(guide.left)} left this month</span><strong>{share === null ? "—" : <><RollingNumber value={share} format={value => String(Math.round(value))} />%</>}</strong></div>
          <div className="design-buy-track"><span style={{ width: `${Math.min(100, Math.max(0, share || 0))}%` }} /></div>
          <p>{price === null ? "add a price to see how it fits." : guide.noBaseline ? "sofar needs a full month of income history before it can say how this fits your month." : after! >= 0 ? `fits. you’d have ${money(after!)} left this month, about ${money(perDayAfter!)} a day.` : `this would put the month ${money(Math.abs(after!))} over your current spending plan.`}</p>
        </section>
      </div>
      <section className="design-buy-invest">
        <p>or, if you invested it instead</p>
        {[1, 5, 10].map(years => <div key={years}><span>in {years} {years === 1 ? "yr" : "yrs"}</span><strong>{price === null ? "—" : <RollingNumber value={investmentProjection(price, rate, years).projected} format={value => money(Math.round(value))} />}</strong></div>)}
        <details><summary>assumptions</summary><label htmlFor="design-buy-rate">hypothetical annual return: {rate}%</label><input id="design-buy-rate" type="range" min="-10" max="15" step=".5" value={rate} onChange={event => setRate(Number(event.target.value))} /><small>compounded annually, before taxes, fees, and inflation. actual returns vary and can be negative.</small></details>
      </section>
    </div>
    <div className="design-buy-actions">
      <button type="button" data-h="soft" onClick={() => price !== null && onSleep?.(item.trim() || "this purchase", price)} disabled={price === null}>sleep on it</button>
      <button type="button" data-h="success" onClick={onDone}>done</button>
    </div>
    <p className="design-buy-note">this is a comparison, not a purchase or an account balance. nothing is added to your transactions.</p>
  </div>;
}
