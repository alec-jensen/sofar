import { ArrowRight, ChevronLeft, ChevronRight, CloudOff, RefreshCw } from "lucide-react";
import { budget, money, nextDue, type Category, type State, type Transaction } from "./model";
import RollingNumber from "./RollingNumber";
import { usePullToSync } from "./usePullToSync";
import { useState, type CSSProperties, type ReactNode } from "react";

type Page = "Transactions" | "Accounts" | "Recurring" | "Savings goal" | "Review inbox" | "Should I buy this" | "Calculators";
type Props = {
  state: State;
  period: Date;
  count: number;
  transactions: Transaction[];
  renderTransaction: (transaction: Transaction) => ReactNode;
  onPage: (page: Page) => void;
  onCategory: (category: Category | "income" | "all", month: string) => void;
  onMonth: (offset: number) => void;
  canGoForward: boolean;
  onLink: () => void;
  onSync?: () => void | Promise<void>;
  syncing?: boolean;
};

export function spendingGuide(state: State, now = new Date()) {
  const b = budget(state, now);
  const daysLeft = new Date(now.getFullYear(), now.getMonth() + 1, 0).getDate() - now.getDate() + 1;
  // The plan averages the last three full months of income; with none yet there is nothing to plan with.
  const noBaseline = !state.demo && b.income === 0;
  const left = noBaseline ? 0 : b.safe - b.totals.spending;
  return { left, daysLeft, perDay: Math.round(left / daysLeft), noBaseline };
}

export const monthKey = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;

export default function Dashboard({ state, period, count, transactions, renderTransaction, onPage, onCategory, onMonth, canGoForward, onLink, onSync, syncing }: Props) {
  const pull = usePullToSync(onSync, syncing);
  const [heroMode, setHeroMode] = useState<"perDay" | "total">(
    () => (localStorage.getItem("sofar-hero-mode") === "total" ? "total" : "perDay"),
  );
  const toggleHeroMode = () => {
    const next = heroMode === "perDay" ? "total" : "perDay";
    setHeroMode(next);
    localStorage.setItem("sofar-hero-mode", next);
  };
  const now = new Date();
  const b = budget(state, now);
  const dayOne = state.transactions.length === 0;
  const spending = spendingGuide(state, now);
  const noBaseline = spending.noBaseline;
  const guide = dayOne ? { ...spending, left: 0, perDay: 0 } : spending;
  const reauth = state.accounts.filter((a) => a.needsReauth);
  const banner = reauth.length
    ? { text: `${reauth[0].institution.toLowerCase()} needs attention in simplefin. balances may be out of date.`, label: "see", action: () => onPage("Accounts") }
    : !state.demo && !state.connection?.connected
      ? { text: "connect your banks to see your first safe-to-spend number.", label: "connect", action: onLink }
      : !state.demo && state.connection?.error
        ? { text: `last sync had a problem: ${state.connection.error.toLowerCase()}`, label: "see", action: () => onPage("Accounts") }
        : null;
  const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
  const upcoming = state.recurring
    .filter(r => r.confirmed && !r.dismissed && r.type === "bill")
    .map(r => ({ ...r, nextDate: nextDue(r, now) }))
    .filter(r => r.nextDate >= today)
    .sort((a, b) => a.nextDate.localeCompare(b.nextDate))
    .slice(0, 3);
  const neverSynced = new Date(state.lastSync).getFullYear() < 2000;
  const syncMinutes = Math.max(0, Math.floor((Date.now() - new Date(state.lastSync).getTime()) / 60000));
  const syncLabel = neverSynced ? "not synced yet" : syncMinutes < 1 ? "synced just now" : syncMinutes < 60 ? `synced ${syncMinutes} min ago` : `synced ${Math.floor(syncMinutes / 60)} hr ago`;
  const goalPercent = Math.min(100, (state.goal.saved / Math.max(1, state.goal.target)) * 100);
  const excluded = new Set(state.accounts.filter((a) => a.excludedFromSafeToSpend).map((a) => a.id));
  const linkedCredits = new Set(state.links.map((l) => l.creditId));
  const incomeMonths = [3, 2, 1].map((back) => {
    const d = new Date(now.getFullYear(), now.getMonth() - back, 1);
    const key = monthKey(d);
    const total = state.transactions
      .filter((t) => t.direction === "in" && t.status === "confirmed" && !t.bankPending && !t.ignored && !excluded.has(t.accountId) && !linkedCredits.has(t.id) && (t.incomeStream === "salary" || t.incomeStream === "self-employed") && t.date.startsWith(key))
      .reduce((n, t) => n + t.amount, 0);
    return { key, label: d.toLocaleDateString("en-US", { month: "short" }).toLowerCase(), total };
  });
  const incomeMax = Math.max(1, ...incomeMonths.map((m) => m.total));
  const pb = budget(state, period);
  const periodKey = monthKey(period);
  const isCurrent = periodKey === monthKey(now);
  const plans: Record<Category, number> = {
    expenses: Math.max(pb.bills, pb.plannedExpenses),
    spending: Math.max(0, pb.safe),
    savings: Math.max(state.goal.monthly, pb.plannedSavings),
  };
  const periodLabel = period.toLocaleDateString("en-US", { month: "long", year: period.getFullYear() === now.getFullYear() ? undefined : "numeric" }).toLowerCase();
  return <div className="reference-home" ref={pull.root} style={{ "--pull-y": `${pull.pull}px` } as CSSProperties}>
    <div className="reference-pull-indicator" aria-hidden="true" style={{ opacity: Math.min(1, pull.pull / 50) }}>{syncing ? "syncing…" : pull.pull > 64 ? "release to sync" : "pull to sync"}</div>
    <div className="reference-home-top">
      <strong className="reference-mobile-brand">sofar<span>.</span></strong>
      <span className="reference-date">{now.toLocaleDateString("en-US", { weekday: "short", month: "short", day: "numeric" }).toLowerCase()}</span>
      <button className="reference-sync" data-h="press" type="button" disabled={syncing} onClick={onSync}><span className="reference-dot" />{syncing ? "syncing…" : syncLabel}<RefreshCw data-sync-spin={syncing} size={13} /></button>
    </div>
    {banner && (
      <button className="reference-status-banner" onClick={banner.action} data-h="tap">
        <CloudOff size={16} />
        <span>{banner.text}</span>
        <span className="reference-status-banner-action">{banner.label}<ArrowRight size={14} /></span>
      </button>
    )}
    <div className="reference-home-grid">
      <div className="reference-hero">
        <button type="button" className="reference-hero-toggle" data-h="tap" onClick={toggleHeroMode}>
          <h1 className="reference-hero-label">{heroMode === "perDay" ? "safe to spend today" : "safe to spend this month"}</h1>
        </button>
        <strong className="reference-hero-number">
          <RollingNumber value={heroMode === "perDay" ? guide.perDay : guide.left} format={value => money(Math.round(value))} />
        </strong>
        <p className="reference-hero-sub">
          {noBaseline ? "sofar plans from your last three full months of income, so your number appears once a full month of paychecks is in. mark your income in review." : heroMode === "perDay"
            ? <>{money(guide.left)} left this month · {guide.daysLeft} {guide.daysLeft === 1 ? "day" : "days"} to go</>
            : <>about {money(guide.perDay)} a day · {guide.daysLeft} {guide.daysLeft === 1 ? "day" : "days"} left</>}
        </p>
        <button className="reference-review" onClick={() => onPage("Review inbox")} aria-label={`${count} to review`}>
          <span className="reference-review-count">{count}</span>
          <span className="reference-review-copy"><strong>{count ? `${count} to review` : "all caught up"}</strong><small>{count ? "about a minute" : "nothing needs your attention"}</small></span>
          <span className="reference-review-go">go</span>
        </button>
        <button className="reference-income" onClick={() => onCategory("income", "all time")} aria-label={`income, last 3 months average ${money(b.income)} a month. ${incomeMonths.map((m) => `${m.label} ${money(m.total)}`).join(", ")}`}>
          <span><small>income, last 3 mo avg</small><strong>{money(b.income)}/mo</strong></span>
          <span className="reference-income-bars" aria-hidden="true">{incomeMonths.map((m) => <i key={m.key} title={`${m.label}: ${money(m.total)}`} style={{ height: `${Math.max(8, (m.total / incomeMax) * 100)}%` }} />)}</span>
        </button>
      </div>
      <div className="reference-home-rail">
        <button className="reference-goal reference-tile" onClick={() => onPage("Savings goal")}>
          <span className="reference-tile-heading"><span>{state.goal.name}</span><small>{goalPercent >= 100 ? "goal reached" : "on your way"}</small></span>
          <span className="reference-goal-number"><strong>{money(state.goal.saved)}</strong> of {money(state.goal.target)}</span>
          <span className="reference-progress"><i style={{ width: `${goalPercent}%` }} /></span>
        </button>
        <button className="reference-upcoming reference-tile" onClick={() => onPage("Recurring")}>
          <span className="reference-tile-heading"><span>coming up</span><small>already set aside</small></span>
          {upcoming.length ? upcoming.map(r => <span className="reference-bill" key={r.id}><span>{r.merchant.toLowerCase()}</span><span>{new Date(r.nextDate + "T12:00:00").toLocaleDateString("en-US", { month: "short", day: "numeric" }).toLowerCase()} · {money(r.amount)}</span></span>) : <span className="reference-bill">no bills coming up</span>}
        </button>
        <button className="reference-buy-link" onClick={() => onPage("Should I buy this")}>
          <span><strong>should i buy this?</strong><small>quick gut-check before you tap pay</small></span><span>open <ArrowRight size={14} /></span>
        </button>
      </div>
    </div>
    <section className="home-month" aria-label="spending by month">
      <div className="home-month-head">
        <h2>{isCurrent ? "this month" : periodLabel}</h2>
        <div className="month-picker">
          <button type="button" aria-label="previous month" data-h="tick" onClick={() => onMonth(-1)}><ChevronLeft size={16} /></button>
          <span>{periodLabel}</span>
          <button type="button" aria-label="next month" data-h="tick" disabled={!canGoForward} onClick={() => onMonth(1)}><ChevronRight size={16} /></button>
        </div>
      </div>
      <div className="home-month-groups">
        {(["expenses", "spending", "savings"] as const).map((c) => {
          const spent = pb.totals[c];
          const plan = plans[c];
          const pct = plan > 0 ? Math.min(100, (spent / plan) * 100) : spent > 0 ? 100 : 0;
          return <button key={c} type="button" className="home-month-group" data-over={plan > 0 && spent > plan} onClick={() => onCategory(c, periodKey)}>
            <span className="home-month-group-top"><strong>{state.categories[c]}</strong><span>{money(spent)} <small>of {plan > 0 ? money(plan) : "no plan"}</small></span></span>
            <span className="reference-progress"><i style={{ width: `${pct}%` }} /></span>
            <small>{plan <= 0 ? "set a plan in categories" : spent <= plan ? `${money(plan - spent)} left` : `${money(spent - plan)} over`}</small>
          </button>;
        })}
      </div>
      <div className="home-month-recent">
        <div className="home-month-head"><h3>recent</h3>{transactions.length > 0 && <button type="button" className="text-button" onClick={() => onCategory("all", periodKey)}>see all {transactions.length}<ArrowRight size={14} /></button>}</div>
        {transactions.length ? <div className="history-day-card">{transactions.slice(0, 5).map(renderTransaction)}</div> : <p className="footnote">no confirmed transactions in {periodLabel}.</p>}
      </div>
    </section>
    <p className="reference-home-note">your daily guide uses this month’s safe-to-spend plan, minus confirmed spending so far. it isn’t your account balance.</p>
  </div>;
}
