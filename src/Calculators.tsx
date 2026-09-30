import { useState } from "react";
import { ArrowRight, CircleHelp, Heart, Landmark, Sprout, TrendingUp } from "lucide-react";
import { budget, money, type State } from "./model";
import { spendingGuide } from "./Dashboard";
import { debtPayoff, emergencyRunway, investmentGrowth, monthsToSave } from "./calculatorMath";

type Tool = "savings" | "emergency" | "debt" | "investing";
const dollars = (value: string) => Math.round(Math.max(0, Math.min(1_000_000, Number(value) || 0)) * 100);
const oneDecimal = (value: number) => new Intl.NumberFormat("en-US", { maximumFractionDigits: 1 }).format(value);

function AmountField({ id, label, value, onChange, hint }: { id: string; label: string; value: string; onChange: (value: string) => void; hint?: string }) {
  return <label className="calc-field" htmlFor={id}>
    <span>{label}</span>
    <span className="calc-input-wrap"><span aria-hidden="true">$</span><input id={id} type="number" min="0" max="1000000" step="0.01" inputMode="decimal" value={value} onChange={event => onChange(event.target.value)} placeholder="0" /></span>
    {hint && <small>{hint}</small>}
  </label>;
}

function NumberField({ id, label, value, onChange, suffix, min, max, step = 1 }: { id: string; label: string; value: string; onChange: (value: string) => void; suffix: string; min: number; max: number; step?: number }) {
  return <label className="calc-field" htmlFor={id}>
    <span>{label}</span>
    <span className="calc-input-wrap calc-number-wrap"><input id={id} type="number" min={min} max={max} step={step} inputMode="decimal" value={value} onChange={event => onChange(event.target.value)} /><span aria-hidden="true">{suffix}</span></span>
  </label>;
}

function RangeAmount({ id, label, value, max, step, onChange }: { id: string; label: string; value: string; max: number; step: number; onChange: (value: string) => void }) {
  const amount = Number(value);
  const sliderValue = Number.isFinite(amount) ? Math.min(max, Math.max(0, amount)) : 0;
  return <div className="calc-range-field">
    <div className="calc-range-heading"><label htmlFor={`${id}-slider`}>{label}</label><label className="calc-range-number" htmlFor={`${id}-number`}><span aria-hidden="true">$</span><input id={`${id}-number`} type="number" inputMode="decimal" min="0" max="1000000" step="0.01" value={value} onChange={event => onChange(event.target.value)} aria-label={`${label} in dollars`} /></label></div>
    <input id={`${id}-slider`} type="range" min="0" max={max} step={step} value={sliderValue} onChange={event => onChange(event.target.value)} aria-label={label} />
  </div>;
}

export default function Calculators({ state, onBuy, onSetup }: { state: State; onBuy: (saved?: { item: string; price: number }) => void; onSetup?: (page: "Categories" | "Sorting rules" | "Recurring" | "Accounts" | "Settings") => void }) {
  const [tool, setTool] = useState<Tool | null>(null);
  const [savedPurchases, setSavedPurchases] = useState<{ item: string; price: number; date: string }[]>(() => {
    try {
      const stored = JSON.parse(localStorage.getItem("sofar-sleep-list") || "[]");
      return Array.isArray(stored) ? stored.filter(item => typeof item?.item === "string" && Number.isFinite(item?.price) && typeof item?.date === "string") : [];
    } catch { return []; }
  });
  const [target, setTarget] = useState(String(state.goal.target / 100));
  const [saved, setSaved] = useState(String(state.goal.saved / 100));
  const [monthly, setMonthly] = useState(String(state.goal.monthly / 100));
  const [cash, setCash] = useState("");
  const [essentials, setEssentials] = useState(String(Math.max(0, budget(state).bills) / 100));
  const [balance, setBalance] = useState("");
  const [apr, setApr] = useState("");
  const [payment, setPayment] = useState("");
  const [extra, setExtra] = useState("");
  const [initial, setInitial] = useState("");
  const [contribution, setContribution] = useState("");
  const [returnRate, setReturnRate] = useState("5");
  const [years, setYears] = useState("10");

  const savingsMonths = monthsToSave(dollars(target), dollars(saved), dollars(monthly));
  const runway = emergencyRunway(dollars(cash), dollars(essentials));
  const validDebt = Number(apr) >= 0 && Number(apr) <= 100 && apr !== "";
  const payoff = balance && payment && validDebt ? debtPayoff(dollars(balance), Number(apr), dollars(payment)) : null;
  const payoffExtra = balance && payment && validDebt && dollars(extra) > 0 ? debtPayoff(dollars(balance), Number(apr), dollars(payment) + dollars(extra)) : null;
  const validInvest = returnRate !== "" && Number(returnRate) >= -20 && Number(returnRate) <= 30 && Number(years) >= 1 && Number(years) <= 50;
  const growth = validInvest ? investmentGrowth(dollars(initial), dollars(contribution), Number(returnRate), Number(years)) : null;

  const tools: { id: Tool; label: string; icon: typeof Sprout }[] = [
    { id: "savings", label: "savings goal", icon: Sprout },
    { id: "emergency", label: "emergency fund", icon: Heart },
    { id: "debt", label: "debt payoff", icon: Landmark },
    { id: "investing", label: "investment growth", icon: TrendingUp },
  ];
  return <div className="calc-layout">
    <button className="calc-buy-link" onClick={() => onBuy()}>
      <span><strong>should i buy this?</strong><small>{spendingGuide(state).noBaseline ? "see how a price compares with your month" : `check a price against today’s ${money(spendingGuide(state).perDay)} before you tap pay`}</small></span>
      <span className="calc-buy-input">what’s it cost? <ArrowRight size={17} /></span>
    </button>
    <span className="calc-section-label">calculators</span>
    <div className="calc-accordions">
      {tools.map(({ id, label }) => <div className="calc-accordion" key={id}>
        <button className="calc-accordion-toggle" aria-expanded={tool === id} aria-controls={`calc-panel-${id}`} onClick={() => setTool(tool === id ? null : id)}>
          <span><strong>{label}</strong><small>{id === "savings" ? `${state.goal.name} · ${savingsMonths === null ? "set a monthly amount" : savingsMonths === 0 ? "goal reached" : `about ${savingsMonths} months to go`}` : id === "emergency" ? "see how long your cushion lasts" : id === "debt" ? "see what extra payments save" : "see what your money could become"}</small></span>
          <span className="calc-accordion-plus">{tool === id ? "×" : "+"}</span>
        </button>
        {tool === id && <section className="calc-panel" id={`calc-panel-${id}`}>
      {tool === "savings" && <>
        <div className="calc-panel-head"><span className="calc-eyebrow">make a little progress</span><h2>savings goal</h2><p>see how long it could take to reach a target with steady monthly contributions.</p></div>
        <RangeAmount id="calc-monthly" label="add each month" value={monthly} max={5000} step={25} onChange={setMonthly} />
        <div className="calc-fields">
          <AmountField id="calc-target" label="target amount" value={target} onChange={setTarget} />
          <AmountField id="calc-saved" label="already saved" value={saved} onChange={setSaved} />
        </div>
        <div className="calc-result" aria-live="polite">
          <span>{savingsMonths === 0 ? "you’ve reached this target" : "estimated time to goal"}</span>
          <strong>{savingsMonths === null ? "add a monthly amount" : savingsMonths === 0 ? "already there" : `${savingsMonths} ${savingsMonths === 1 ? "month" : "months"}`}</strong>
          <small>{money(Math.max(0, dollars(target) - dollars(saved)), true)} still to save · no interest assumed</small>
        </div>
        <p className="calc-note">these values started with your current savings goal. changing them here won’t change the goal in your workspace.</p>
      </>}
      {tool === "emergency" && <>
        <div className="calc-panel-head"><span className="calc-eyebrow">a little cushion</span><h2>emergency fund</h2><p>compare cash set aside with your essential monthly costs.</p></div>
        <RangeAmount id="calc-cash" label="cash set aside" value={cash} max={100000} step={100} onChange={setCash} />
        <div className="calc-fields">
          <AmountField id="calc-essentials" label="monthly essentials" value={essentials} onChange={setEssentials} hint="we started with recurring bills. include groceries and other essentials too." />
        </div>
        <div className="calc-result" aria-live="polite">
          <span>estimated expenses covered</span>
          <strong>{runway ? `${oneDecimal(runway.months)} months` : "add monthly essentials"}</strong>
          {runway && <small>{money(runway.threeMonthGap, true)} more for 3 months · {money(runway.sixMonthGap, true)} more for 6 months</small>}
        </div>
        <p className="calc-note">a simple cash runway, without interest or changes to your expenses. choose a cushion that fits your life.</p>
      </>}
      {tool === "debt" && <>
        <div className="calc-panel-head"><span className="calc-eyebrow">see the path forward</span><h2>debt payoff</h2><p>estimate how long a fixed monthly payment could take, and how an extra payment changes it.</p></div>
        <RangeAmount id="calc-extra" label="extra each month" value={extra} max={5000} step={25} onChange={setExtra} />
        <div className="calc-fields">
          <AmountField id="calc-balance" label="balance owed" value={balance} onChange={setBalance} />
          <NumberField id="calc-apr" label="annual interest rate" value={apr} onChange={setApr} suffix="%" min={0} max={100} step={0.1} />
          <AmountField id="calc-payment" label="monthly payment" value={payment} onChange={setPayment} />
        </div>
        <div className="calc-result" aria-live="polite">
          <span>estimated payoff time</span>
          <strong>{!balance || !payment || !apr ? "add balance, rate, and payment" : !validDebt ? "check the interest rate" : payoff ? `${payoff.months} ${payoff.months === 1 ? "month" : "months"}` : "not paid off within 50 years at this payment"}</strong>
          {payoff && <small>{money(payoff.interest, true)} estimated interest at your regular payment</small>}
          {payoffExtra && <small>with {money(dollars(extra), true)} extra: {payoffExtra.months} {payoffExtra.months === 1 ? "month" : "months"} and {money(payoffExtra.interest, true)} interest{payoff ? ` · save ${Math.max(0, payoff.months - payoffExtra.months)} months and ${money(Math.max(0, payoff.interest - payoffExtra.interest), true)}` : ""}</small>}
        </div>
        <p className="calc-note">assumes a fixed rate, monthly compounding, and payments at month end. actual loan terms, fees, and minimum payments may differ.</p>
      </>}
      {tool === "investing" && <>
        <div className="calc-panel-head"><span className="calc-eyebrow">look a little further out</span><h2>investment growth</h2><p>explore a hypothetical starting amount and regular monthly contributions.</p></div>
        <RangeAmount id="calc-contribution" label="add each month" value={contribution} max={5000} step={25} onChange={setContribution} />
        <div className="calc-fields">
          <AmountField id="calc-initial" label="starting amount" value={initial} onChange={setInitial} />
          <NumberField id="calc-return" label="assumed annual return" value={returnRate} onChange={setReturnRate} suffix="%" min={-20} max={30} step={0.5} />
          <NumberField id="calc-years" label="years invested" value={years} onChange={setYears} suffix="years" min={1} max={50} />
        </div>
        <div className="calc-result" aria-live="polite">
          <span>hypothetical future value</span>
          <strong>{growth ? money(growth.value, true) : "check your assumptions"}</strong>
          {growth && <small>{money(growth.contributed, true)} contributed · {growth.growth >= 0 ? "+" : "−"}{money(Math.abs(growth.growth), true)} {growth.growth >= 0 ? "growth" : "change"}</small>}
        </div>
        <p className="calc-note"><CircleHelp size={14} /> compounded monthly, with contributions at month end. before taxes, fees, and inflation. actual returns vary and can be negative; this is not a forecast.</p>
      </>}
        </section>}
      </div>)}
    </div>
    {savedPurchases.length > 0 && <>
      <span className="calc-section-label">saved for later</span>
      <div className="calc-saved-list">{savedPurchases.map((saved, index) => <div key={`${saved.date}-${index}`}><span><strong>{saved.item}</strong><small>{money(saved.price, true)} · saved {new Date(saved.date).toLocaleDateString("en-US", { month: "short", day: "numeric" }).toLowerCase()}</small></span><button aria-label={`check ${saved.item} again`} onClick={() => onBuy({ item: saved.item, price: saved.price })}>check again</button><button aria-label={`remove ${saved.item}`} onClick={() => { const next = savedPurchases.filter((_, i) => i !== index); setSavedPurchases(next); localStorage.setItem("sofar-sleep-list", JSON.stringify(next)); }}>remove</button></div>)}</div>
    </>}
    <span className="calc-section-label">setup</span>
    <div className="calc-setup-links">
      <button onClick={() => onSetup?.("Categories")}><span><strong>categories</strong><small>{(state.subcategories || []).length} {(state.subcategories || []).length === 1 ? "category" : "categories"} across three groups</small></span><ArrowRight size={18} /></button>
      <button onClick={() => onSetup?.("Sorting rules")}><span><strong>sorting rules</strong><small>{state.rules.length} {state.rules.length === 1 ? "rule" : "rules"} from your reviews</small></span><ArrowRight size={18} /></button>
      <button onClick={() => onSetup?.("Recurring")}><span><strong>subscriptions & recurring</strong><small>{state.recurring.filter(r => r.confirmed && !r.dismissed && r.type === "bill").length} tracked · see what’s coming up</small></span><ArrowRight size={18} /></button>
      <button onClick={() => onSetup?.("Accounts")}><span><strong>accounts</strong><small>connections and balances</small></span><ArrowRight size={18} /></button>
      <button onClick={() => onSetup?.("Settings")}><span><strong>settings</strong><small>preferences and security</small></span><ArrowRight size={18} /></button>
    </div>
  </div>;
}
