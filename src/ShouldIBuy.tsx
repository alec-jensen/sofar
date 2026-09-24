import { useState } from "react";
import { ArrowRight, CircleHelp, Sprout } from "lucide-react";
import { budget, money, type State } from "./model";
import { investmentProjection, purchaseImpact } from "./purchaseMath";

const oneDecimal = (value: number) =>
  new Intl.NumberFormat("en-US", { maximumFractionDigits: 1 }).format(value);

export default function ShouldIBuy({ state, onExplore }: { state: State; onExplore?: () => void }) {
  const [priceText, setPriceText] = useState("");
  const [item, setItem] = useState("");
  const [years, setYears] = useState(10);
  const [rate, setRate] = useState(5);
  const priceNumber = Number(priceText);
  const price =
    priceText.trim() !== "" &&
    Number.isFinite(priceNumber) &&
    priceNumber >= 0.01 &&
    priceNumber <= 1_000_000
      ? Math.round(priceNumber * 100)
      : null;
  const now = new Date();
  const b = budget(state, now);
  const daysInMonth = new Date(now.getFullYear(), now.getMonth() + 1, 0).getDate();
  const impact =
    price === null
      ? null
      : purchaseImpact(price, b.safe, b.totals.spending, daysInMonth);
  const projection =
    price === null ? null : investmentProjection(price, rate, years);
  const goalRemaining = Math.max(0, state.goal.target - state.goal.saved);
  const purchaseName = item.trim() || "this purchase";

  return (
    <div className="buy-layout">
      {onExplore && <button className="calc-buy-link" onClick={onExplore}>
        <span className="calc-buy-icon"><Sprout size={19} /></span>
        <span><strong>more calculators</strong><small>explore savings, emergency funds, debt, and investing</small></span>
        <ArrowRight size={17} />
      </button>}
      <section className="card buy-entry">
        <div className="buy-intro">
          <span className="buy-kicker">a little pause before you decide</span>
          <h2>what are you thinking of buying?</h2>
          <p>see what it would mean for your plan. it’s your call, always.</p>
        </div>
        <div className="buy-fields">
          <label className="buy-field" htmlFor="buy-item">
            <span>what is it? <small>optional</small></span>
            <input
              id="buy-item"
              autoComplete="off"
              maxLength={60}
              placeholder="e.g. a weekend away"
              value={item}
              onChange={(event) => setItem(event.target.value)}
            />
          </label>
          <label className="buy-field buy-price-field" htmlFor="buy-price">
            <span>price</span>
            <span className="buy-price-wrap">
              <span aria-hidden="true">$</span>
              <input
                id="buy-price"
                type="number"
                inputMode="decimal"
                min="0.01"
                max="1000000"
                step="0.01"
                placeholder="0.00"
                value={priceText}
                onChange={(event) => setPriceText(event.target.value)}
              />
            </span>
          </label>
        </div>
        {priceText && price === null && (
          <p className="buy-error" role="alert">enter a price between $0.01 and $1,000,000.</p>
        )}
        <p className="buy-private"><CircleHelp size={15} /> this is just a comparison. nothing is added to your transactions.</p>
      </section>

      {impact && projection && price !== null ? (
        <div className="buy-results" aria-live="polite">
          <section className="card buy-impact">
            <div className="buy-section-head">
              <div>
                <span className="buy-kicker">your budget</span>
                <h2>where {purchaseName} fits</h2>
              </div>
              <span className="buy-date">this month</span>
            </div>
            <div className="buy-metric-main">
              <span>your monthly safe-to-spend plan</span>
              <strong>{money(b.safe, true)}</strong>
            </div>
            {impact.shareOfAllowance !== null ? (
              <>
                <div className="buy-bar" role="img" aria-label={`${oneDecimal(impact.shareOfAllowance)} percent of your monthly safe-to-spend plan`}>
                  <span style={{ width: `${Math.min(100, impact.shareOfAllowance)}%` }} />
                </div>
                <div className="buy-two-metrics">
                  <div>
                    <strong>{oneDecimal(impact.shareOfAllowance)}%</strong>
                    <span>of your monthly plan</span>
                  </div>
                  <div>
                    <strong>{oneDecimal(impact.daysOfAllowance!)}</strong>
                    <span>days of your per-day guide</span>
                  </div>
                </div>
              </>
            ) : (
              <p className="buy-context">your monthly safe-to-spend plan is {b.safe < 0 ? "below zero" : "zero"}, so there isn’t an allowance to compare this price with yet.</p>
            )}
            <div className="buy-ledger">
              <div><span>monthly plan</span><strong>{money(b.safe, true)}</strong></div>
              <div><span>confirmed day-to-day spending</span><strong>− {money(b.totals.spending, true)}</strong></div>
              <div><span>this purchase</span><strong>− {money(price, true)}</strong></div>
              <div className="buy-ledger-total"><span>planning room after</span><strong>{money(impact.roomAfter, true)}</strong></div>
            </div>
            <p className="buy-footnote">planning room is based on your monthly allowance and confirmed day-to-day spending so far. it isn’t an account balance.</p>
          </section>

          <section className="card buy-invest">
            <div className="buy-section-head">
              <div>
                <span className="buy-kicker">the other possibility</span>
                <h2>what if you invested it?</h2>
              </div>
              <span className="buy-invest-icon"><Sprout size={21} /></span>
            </div>
            <p className="buy-invest-lead">a one-time {money(price, true)} investment could grow to this amount under your assumptions:</p>
            <div className="buy-projection">
              <span>hypothetical value after {years} {years === 1 ? "year" : "years"}</span>
              <strong>{money(projection.projected, true)}</strong>
              <small>{projection.growth >= 0 ? "+" : "−"}{money(Math.abs(projection.growth), true)} {projection.growth >= 0 ? "growth" : "change"} compared with the original amount</small>
            </div>
            <div className="buy-assumptions">
              <label htmlFor="buy-years"><span>time invested</span><strong>{years} {years === 1 ? "year" : "years"}</strong></label>
              <input id="buy-years" type="range" min="1" max="40" step="1" value={years} onChange={(event) => setYears(Number(event.target.value))} />
              <label htmlFor="buy-rate"><span>assumed annual return</span><strong>{rate}%</strong></label>
              <input id="buy-rate" type="range" min="-10" max="15" step="0.5" value={rate} onChange={(event) => setRate(Number(event.target.value))} />
            </div>
            <p className="buy-footnote">hypothetical compound growth, with no extra contributions. before taxes, fees, and inflation. actual returns can be lower or negative; this is not a forecast.</p>
            <a className="buy-source" href="https://www.investor.gov/introduction-investing/general-resources/news-alerts/alerts-bulletins/investor-bulletins-47" target="_blank" rel="noopener noreferrer">about investment projections <ArrowRight size={14} /></a>
          </section>

          {goalRemaining > 0 && (
            <section className="card buy-goal">
              <span className="buy-goal-icon"><Sprout size={18} /></span>
              <div>
                <h2>your {state.goal.name.toLowerCase()} goal</h2>
                <p>{money(goalRemaining, true)} left to reach it. {money(price, true)} is {oneDecimal((price / goalRemaining) * 100)}% of that remaining amount.</p>
              </div>
            </section>
          )}
        </div>
      ) : (
        <div className="card buy-empty">
          <span className="buy-empty-symbol">✳</span>
          <h2>start with a price</h2>
          <p>we’ll compare it with your spending plan, your per-day guide, your goal, and a hypothetical investment.</p>
        </div>
      )}
    </div>
  );
}
