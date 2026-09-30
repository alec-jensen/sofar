import { useState } from "react";
import { AlertCircle, ArrowRight, Check, ChevronDown, Repeat2, Settings2 } from "lucide-react";
import { money, nextDue, normalize, type Action, type Recurring, type State, type Transaction } from "./model";
import { lookupMerchant } from "./merchants";

type Kind = "subscription" | "bill" | "income";
type Tab = "all" | Kind;
const perMonth = { weekly: 52 / 12, biweekly: 26 / 12, monthly: 1, annual: 1 / 12 };
const cadenceDays = { weekly: 7, biweekly: 14, monthly: 30.44, annual: 365.25 };
const cadenceLabel = { weekly: "weekly", biweekly: "every 2 weeks", monthly: "monthly", annual: "yearly" };
const day = (iso: string) => new Date(iso + "T12:00:00");
const short = (iso: string) => day(iso).toLocaleDateString("en-US", { month: "short", day: "numeric" }).toLowerCase();
const localISO = (d: Date) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;

type Item = {
  r: Recurring;
  name: string;
  kind: Kind;
  charges: Transaction[];
  next: string;
  monthly: number;
  status: "review" | "changed" | "quiet" | "active";
};

function describe(state: State, r: Recurring, today: Date): Item {
  const pattern = normalize(r.merchant);
  let charges = state.transactions.filter((t) => t.recurringId === r.id);
  if (!charges.length)
    charges = state.transactions.filter((t) => t.direction === (r.type === "income" ? "in" : "out") && !t.ignored && normalize(t.merchant).includes(pattern));
  charges = [...charges].sort((a, b) => b.date.localeCompare(a.date));
  const name = charges[0]?.merchant || r.merchant;
  const kind: Kind = r.type === "income" ? "income" : lookupMerchant(name)?.sub === "subscriptions" || charges[0]?.subcategoryId === "subscriptions" ? "subscription" : "bill";
  const sinceLast = charges[0] ? (today.getTime() - day(charges[0].date).getTime()) / 86400000 : 0;
  const status = !r.confirmed ? "review" : r.mismatch ? "changed" : charges[0] && sinceLast > cadenceDays[r.cadence] * 1.6 ? "quiet" : "active";
  return { r, name, kind, charges, next: nextDue(r, today), monthly: Math.round(r.amount * perMonth[r.cadence]), status };
}

// Every due date for an item between today and `until`, for the upcoming list.
function occurrences(item: Item, today: Date, until: Date) {
  const dates: string[] = [];
  const d = day(item.next);
  for (let i = 0; i < 10 && d <= until; i++) {
    if (d >= new Date(today.getFullYear(), today.getMonth(), today.getDate())) dates.push(localISO(d));
    if (item.r.cadence === "weekly") d.setDate(d.getDate() + 7);
    else if (item.r.cadence === "biweekly") d.setDate(d.getDate() + 14);
    else d.setMonth(d.getMonth() + (item.r.cadence === "annual" ? 12 : 1));
  }
  return dates;
}

export default function RecurringPage({
  state,
  onAction,
  onEdit,
  onHistory,
}: {
  state: State;
  onAction: (action: Omit<Action, "id">, message?: string) => Promise<boolean>;
  onEdit: (r: Recurring) => void;
  onHistory: (merchant: string) => void;
}) {
  const [tab, setTab] = useState<Tab>(() => {
    try {
      const saved = localStorage.getItem("sofar-recurring-tab");
      return saved === "subscription" || saved === "bill" || saved === "income" ? saved : "all";
    } catch {
      return "all";
    }
  });
  const [sort, setSort] = useState<"next" | "cost" | "name">("next");
  const [open, setOpen] = useState<string | null>(null);
  const today = new Date();
  const items = state.recurring.filter((r) => !r.dismissed).map((r) => describe(state, r, today));
  const confirmed = items.filter((i) => i.status !== "review");
  const outgoing = confirmed.filter((i) => i.kind !== "income");
  const monthlyCost = outgoing.reduce((n, i) => n + i.monthly, 0);
  const subscriptions = outgoing.filter((i) => i.kind === "subscription");
  const in30 = new Date(today);
  in30.setDate(in30.getDate() + 30);
  const upcoming = outgoing
    .flatMap((i) => occurrences(i, today, in30).map((date) => ({ i, date })))
    .sort((a, b) => a.date.localeCompare(b.date));
  const toReview = items.filter((i) => i.status === "review" || i.status === "changed");
  const shown = confirmed
    .filter((i) => tab === "all" || i.kind === tab)
    .sort((a, b) => (sort === "cost" ? b.monthly - a.monthly : sort === "name" ? a.name.localeCompare(b.name) : a.next.localeCompare(b.next)));
  const hidden = state.recurring.filter((r) => r.dismissed);
  const chooseTab = (next: Tab) => {
    setTab(next);
    try {
      localStorage.setItem("sofar-recurring-tab", next);
    } catch {
      /* remembering the tab is optional */
    }
  };
  const confirm = (i: Item) =>
    i.kind === "income"
      ? onEdit(i.r)
      : onAction({ type: "recurring", recurringId: i.r.id, category: i.r.category, tolerance: i.r.tolerance, amount: i.r.amount }, `${i.name} is now tracked.`);

  return (
    <div className="subs-page">
      <div className="subs-summary">
        <div>
          <span>recurring costs</span>
          <strong>{money(monthlyCost)}<small>/mo</small></strong>
          <small>{money(monthlyCost * 12)} a year</small>
        </div>
        <div>
          <span>subscriptions</span>
          <strong>{money(subscriptions.reduce((n, i) => n + i.monthly, 0))}<small>/mo</small></strong>
          <small>{subscriptions.length} {subscriptions.length === 1 ? "service" : "services"}</small>
        </div>
        <div>
          <span>next 30 days</span>
          <strong>{money(upcoming.reduce((n, u) => n + u.i.r.amount, 0))}</strong>
          <small>{upcoming.length} {upcoming.length === 1 ? "charge" : "charges"}</small>
        </div>
      </div>

      {toReview.length > 0 && (
        <section className="card subs-review">
          <h2>worth a look</h2>
          {toReview.map((i) => (
            <div className="subs-review-row" key={i.r.id}>
              <span className="subs-logo">{i.name.charAt(0)}</span>
              <span className="grow">
                <strong>{i.name.toLowerCase()}</strong>
                <small>
                  {i.status === "changed"
                    ? `amount changed from ${money(i.r.amount, true)}${i.charges[0] ? ` to ${money(i.charges[0].amount, true)}` : ""}`
                    : `looks ${cadenceLabel[i.r.cadence]} · ${money(i.r.amount, true)}${i.charges.length ? ` · seen ${i.charges.length}×` : ""}`}
                </small>
              </span>
              <button className="button small-button" onClick={() => onEdit(i.r)}>edit</button>
              <button className="button small-button" onClick={() => onAction({ type: "dismiss-recurring", recurringId: i.r.id }, "Hidden. You can bring it back below.")}>not recurring</button>
              <button className="button primary small-button" onClick={() => confirm(i)}>
                <Check size={15} /> {i.status === "changed" ? "keep tracking" : "track it"}
              </button>
            </div>
          ))}
        </section>
      )}

      {upcoming.length > 0 && (
        <section className="card subs-upcoming">
          <h2>coming up</h2>
          <div className="subs-timeline">
            {upcoming.slice(0, 8).map(({ i, date }) => (
              <div key={i.r.id + date}>
                <span className="subs-date">
                  <strong>{day(date).getDate()}</strong>
                  <small>{day(date).toLocaleDateString("en-US", { month: "short" }).toLowerCase()}</small>
                </span>
                <span className="grow">{i.name.toLowerCase()}</span>
                <strong>{money(i.r.amount, true)}</strong>
              </div>
            ))}
          </div>
        </section>
      )}

      <div className="subs-toolbar">
        <div className="history-chips" role="tablist" aria-label="recurring type">
          {(["all", "subscription", "bill", "income"] as Tab[]).map((t) => (
            <button key={t} role="tab" aria-selected={tab === t} data-h="tick" className={tab === t ? "active" : ""} onClick={() => chooseTab(t)}>
              {t === "all" ? "all" : t === "subscription" ? "subscriptions" : t === "bill" ? "bills" : "income"}
              <span className="subs-count">{t === "all" ? confirmed.length : confirmed.filter((i) => i.kind === t).length}</span>
            </button>
          ))}
        </div>
        <label className="subs-sort">
          sort
          <select value={sort} onChange={(e) => setSort(e.target.value as typeof sort)}>
            <option value="next">next charge</option>
            <option value="cost">cost</option>
            <option value="name">name</option>
          </select>
        </label>
      </div>

      <section className="card subs-list">
        {shown.map((i) => {
          const expanded = open === i.r.id;
          return (
            <div className={`subs-item ${expanded ? "open" : ""}`} key={i.r.id}>
              <button className="subs-row" aria-expanded={expanded} onClick={() => setOpen(expanded ? null : i.r.id)}>
                <span className="subs-logo">{i.name.charAt(0)}</span>
                <span className="grow">
                  <strong>{i.name.toLowerCase()}</strong>
                  <small>
                    {cadenceLabel[i.r.cadence]} · {i.kind === "income" ? (i.r.incomeStream === "self-employed" ? "self-employed" : "paycheck") : i.kind === "subscription" ? "subscription" : state.categories[i.r.category]}
                    {i.status === "quiet" && <span className="subs-flag"> · no charge since {short(i.charges[0].date)}</span>}
                  </small>
                </span>
                <span className="subs-next">
                  <small>{i.kind === "income" ? "expected" : "next"}</small>
                  {short(i.next)}
                </span>
                <span className="subs-amount">
                  <strong>{money(i.r.amount, true)}</strong>
                  {i.r.cadence !== "monthly" && <small>{money(i.monthly)}/mo</small>}
                </span>
                <ChevronDown size={16} className="subs-chevron" />
              </button>
              {expanded && (
                <div className="subs-detail">
                  {i.status === "quiet" && (
                    <p className="subs-quiet">
                      <AlertCircle size={15} /> no charge in a while. if you canceled it, stop tracking so it isn’t set aside.
                    </p>
                  )}
                  {i.charges.length ? (
                    <div className="subs-charges">
                      {i.charges.slice(0, 6).map((t) => (
                        <div key={t.id}>
                          <span>{short(t.date)}</span>
                          <strong>{money(t.amount, true)}</strong>
                        </div>
                      ))}
                      {i.charges.length > 1 && (
                        <p>
                          average {money(Math.round(i.charges.reduce((n, t) => n + t.amount, 0) / i.charges.length), true)} over {i.charges.length} charges · {i.r.tolerance}% tolerance
                        </p>
                      )}
                    </div>
                  ) : (
                    <p className="subs-quiet">no matching charges yet. sofar will link them when they post.</p>
                  )}
                  <div className="subs-actions">
                    <button className="button small-button" onClick={() => onEdit(i.r)}>
                      <Settings2 size={15} /> edit
                    </button>
                    <button className="button small-button" onClick={() => onHistory(i.name)}>
                      history <ArrowRight size={15} />
                    </button>
                    <button className="text-button account-disconnect" onClick={() => onAction({ type: "dismiss-recurring", recurringId: i.r.id }, `${i.name} is no longer tracked.`)}>
                      {i.status === "quiet" ? "canceled, stop tracking" : "stop tracking"}
                    </button>
                  </div>
                </div>
              )}
            </div>
          );
        })}
        {!shown.length && (
          <div className="empty">
            <Repeat2 />
            <h3>{tab === "all" ? "nothing recurring yet." : `no ${tab === "subscription" ? "subscriptions" : tab === "bill" ? "bills" : "recurring income"} yet.`}</h3>
            <p>
              sofar spots repeating charges after {state.threshold} similar ones, and known subscriptions after the first. you can also add one yourself.
            </p>
          </div>
        )}
      </section>

      {hidden.length > 0 && (
        <details className="card dismissed-patterns">
          <summary>not tracking ({hidden.length})</summary>
          {hidden.map((r) => (
            <div className="recurring-row" key={r.id}>
              <span className="subs-logo">{r.merchant.charAt(0)}</span>
              <div className="grow">
                <strong>{r.merchant}</strong>
                <span>{cadenceLabel[r.cadence]} · {money(r.amount, true)}</span>
              </div>
              <button className="button small-button" onClick={() => onAction({ type: "restore-recurring", recurringId: r.id }, "Tracking again.")}>track again</button>
            </div>
          ))}
        </details>
      )}
      <p className="footnote">
        bills in {state.categories.expenses} are set aside before your safe-to-spend number. subscriptions count as {state.categories.expenses} unless you move them. income here is a pattern, not money you can spend until it arrives.
      </p>
    </div>
  );
}
