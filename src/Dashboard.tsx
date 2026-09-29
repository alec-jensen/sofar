import { ArrowRight, CloudOff, RefreshCw } from "lucide-react";
import { budget, money, type Category, type State, type Transaction } from "./model";
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
  onCategory: (category: Category) => void;
  onLink: () => void;
  onSync?: () => void | Promise<void>;
  syncing?: boolean;
};

export function spendingGuide(state: State, now = new Date()) {
  const b = budget(state, now);
  const daysLeft = new Date(now.getFullYear(), now.getMonth() + 1, 0).getDate() - now.getDate() + 1;
  const left = b.safe - b.totals.spending;
  return { left, daysLeft, perDay: Math.round(left / daysLeft) };
}

export default function Dashboard({ state, count, onPage, onLink, onSync, syncing }: Props) {
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
  const guide = spendingGuide(state, now);
  const reauth = state.accounts.filter((a) => a.needsReauth);
  const banner = reauth.length
    ? { text: `we lost the connection to ${reauth[0].institution.toLowerCase()}. balances may be out of date.`, label: "reconnect", action: () => onPage("Accounts") }
    : !state.demo && !state.transactions.length
      ? { text: "connect an account to see your first safe-to-spend number.", label: "connect", action: onLink }
      : null;
  const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}-${String(now.getDate()).padStart(2, "0")}`;
  const upcoming = state.recurring
    .filter(r => r.confirmed && !r.dismissed && r.type === "bill" && r.nextDate >= today)
    .sort((a, b) => a.nextDate.localeCompare(b.nextDate))
    .slice(0, 3);
  const syncMinutes = Math.max(0, Math.floor((Date.now() - new Date(state.lastSync).getTime()) / 60000));
  const syncLabel = syncMinutes < 1 ? "synced just now" : syncMinutes < 60 ? `synced ${syncMinutes} min ago` : `synced ${Math.floor(syncMinutes / 60)} hr ago`;
  const goalPercent = Math.min(100, (state.goal.saved / Math.max(1, state.goal.target)) * 100);
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
          <h1 className="reference-hero-label">{heroMode === "perDay" ? "safe to spend today" : "safe to spend till payday"}</h1>
        </button>
        <strong className="reference-hero-number">
          <RollingNumber value={heroMode === "perDay" ? guide.perDay : guide.left} format={value => money(Math.round(value))} />
        </strong>
        <p className="reference-hero-sub">
          {heroMode === "perDay"
            ? <>{money(guide.left)} left till payday · {guide.daysLeft} {guide.daysLeft === 1 ? "day" : "days"}</>
            : <>about {money(guide.perDay)} a day · {guide.daysLeft} {guide.daysLeft === 1 ? "day" : "days"} left</>}
        </p>
        <button className="reference-review" onClick={() => onPage("Review inbox")} aria-label={`${count} to review`}>
          <span className="reference-review-count">{count}</span>
          <span className="reference-review-copy"><strong>{count ? `${count} to review` : "all caught up"}</strong><small>{count ? "about a minute" : "nothing needs your attention"}</small></span>
          <span className="reference-review-go">go</span>
        </button>
        <button className="reference-income" onClick={() => onPage("Transactions")}>
          <span><small>income, last 3 mo avg</small><strong>{money(b.income)}/mo</strong></span>
          <span className="reference-income-bars" aria-hidden="true"><i /><i /><i /></span>
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
    <p className="reference-home-note">your daily guide uses this month’s safe-to-spend plan, minus confirmed spending so far. it isn’t your account balance.</p>
  </div>;
}
