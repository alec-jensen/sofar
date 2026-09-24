import type { ReactNode } from "react";
import {
  ArrowRight,
  ChevronRight,
  CircleHelp,
  Home,
  Inbox,
  Landmark,
  Plus,
  Repeat2,
  ShoppingBag,
  Sprout,
  Wallet,
} from "lucide-react";
import {
  budget,
  dailyAllowance,
  money,
  type Category,
  type State,
  type Transaction,
} from "./model";

type Props = {
  state: State;
  period: Date;
  count: number;
  transactions: Transaction[];
  renderTransaction: (transaction: Transaction) => ReactNode;
  onPage: (
    page:
      | "Transactions"
      | "Accounts"
      | "Recurring"
      | "Savings goal"
      | "Review inbox"
      | "Should I buy this",
  ) => void;
  onCategory: (category: Category) => void;
  onLink: () => void;
};

export default function Dashboard({
  state,
  period,
  count,
  transactions,
  renderTransaction,
  onPage,
  onCategory,
  onLink,
}: Props) {
  const b = budget(state, period);
  const daysInMonth = new Date(
    period.getFullYear(),
    period.getMonth() + 1,
    0,
  ).getDate();
  const committed = Math.max(0, b.bills);
  const savings = Math.max(0, state.goal.monthly);
  const free = Math.max(0, b.safe);
  const total = Math.max(1, committed + savings + free);
  const upcoming = state.recurring
    .filter(
      (r) =>
        r.confirmed &&
        !r.dismissed &&
        r.type === "bill" &&
        r.nextDate >= new Date().toLocaleDateString("en-CA"),
    )
    .sort((a, b) => a.nextDate.localeCompare(b.nextDate))
    .slice(0, 3);
  const categoryRows: {
    id: Category;
    icon: typeof Home;
    description: string;
  }[] = [
    { id: "expenses", icon: Home, description: "bills and essentials" },
    { id: "spending", icon: Wallet, description: "day to day" },
    { id: "savings", icon: Sprout, description: "set aside" },
  ];
  return (
    <div className="dashboard-layout">
      <div className="dashboard-primary">
        <section className="dashboard-card money-summary">
          <div className="money-hero">
            <div className="dashboard-card-heading">
              <h2>safe to spend</h2>
              <details className="budget-explanation">
                <summary aria-label="how safe to spend works">
                  <CircleHelp size={17} />
                </summary>
                <div>
                  <strong>here’s the math</strong>
                  <p>
                    your average confirmed income from the last three complete
                    months, minus recurring bills and your monthly savings
                    contribution.
                  </p>
                  <p>
                    salary and self-employment stay separate. transfers,
                    repayments, and unreviewed income aren’t included.
                  </p>
                  <p>
                    this is a monthly allowance. your current account balances
                    are shown separately.
                  </p>
                  <p>
                    the per-day guide spreads that monthly amount evenly across
                    the {daysInMonth} days in this month.
                  </p>
                </div>
              </details>
            </div>
            <div className="dashboard-amount">
              {money(b.safe, true).slice(0, -3)}
              <span>.{String(Math.abs(b.safe) % 100).padStart(2, "0")}</span>
            </div>
            <p className="dashboard-caption">after bills and savings</p>
            <div className="daily-allowance">
              <strong>{money(dailyAllowance(b.safe, period), true)}</strong>
              <span>per day</span>
            </div>
          </div>
          <div
            className="allocation-bar"
            role="img"
            aria-label={`${money(b.bills)} in bills, ${money(state.goal.monthly)} for savings, ${money(b.safe)} safe to spend`}
          >
            <i
              className="allocation-bills"
              style={{ width: `${(committed / total) * 100}%` }}
            />
            <i
              className="allocation-savings"
              style={{ width: `${(savings / total) * 100}%` }}
            />
            <i
              className="allocation-free"
              style={{ width: `${(free / total) * 100}%` }}
            />
          </div>
          <div className="allocation-labels">
            <div>
              <span>
                <i className="allocation-bills" />
                bills
              </span>
              <strong>{money(b.bills)}</strong>
            </div>
            <div>
              <span>
                <i className="allocation-savings" />
                savings
              </span>
              <strong>{money(state.goal.monthly)}</strong>
            </div>
            <div>
              <span>
                <i className="allocation-free" />
                available
              </span>
              <strong>{money(b.safe)}</strong>
            </div>
          </div>
          <div className="income-summary">
            <div>
              <span>salary average</span>
              <strong>
                {money(b.salary)}
                <small> / mo</small>
              </strong>
            </div>
            <div>
              <span>self-employed average</span>
              <strong>
                {money(b.selfEmployed)}
                <small> / mo</small>
              </strong>
            </div>
          </div>
        </section>
        <button className="buy-prompt" onClick={() => onPage("Should I buy this")}>
          <span className="buy-prompt-icon"><ShoppingBag size={19} /></span>
          <span><strong>should i buy this?</strong><small>see how a price fits your plan</small></span>
          <ArrowRight size={18} />
        </button>
        {count > 0 && (
          <button
            className="quiet-review"
            onClick={() => onPage("Review inbox")}
          >
            <Inbox size={18} />
            <span>{count} things to review</span>
            <ArrowRight size={17} />
          </button>
        )}
        <section className="dashboard-card category-summary">
          <div className="dashboard-card-heading">
            <h2>this month</h2>
            <span>confirmed so far</span>
          </div>
          <div className="month-total">
            <span>spent so far</span>
            <strong>{money(b.totals.expenses + b.totals.spending, true)}</strong>
          </div>
          {categoryRows.map(({ id, icon: Icon, description }) => (
            <button
              key={id}
              className="category-summary-row"
              onClick={() => onCategory(id)}
            >
              <span className={`category-symbol ${id}`}>
                <Icon size={18} />
              </span>
              <span className="category-summary-label">
                <strong>{state.categories[id]}</strong>
                <small>{description}</small>
              </span>
              <strong>{money(b.totals[id], true)}</strong>
              <ChevronRight size={15} />
            </button>
          ))}
        </section>
        <section className="dashboard-card dashboard-transactions">
          <div className="dashboard-card-heading">
            <h2>recent transactions</h2>
            <button
              className="text-button"
              onClick={() => onPage("Transactions")}
            >
              view all
              <ArrowRight size={14} />
            </button>
          </div>
          {transactions.length ? (
            transactions.slice(0, 5).map(renderTransaction)
          ) : (
            <div className="dashboard-empty">nothing here yet.</div>
          )}
        </section>
      </div>
      <aside className="dashboard-rail">
        <section className="dashboard-card dashboard-accounts">
          <div className="dashboard-card-heading">
            <h2>accounts</h2>
            <button
              className="icon-button"
              aria-label="connect an account"
              onClick={onLink}
            >
              <Plus size={18} />
            </button>
          </div>
          {state.accounts.map((a) => (
            <button
              className="dashboard-account"
              key={a.id}
              onClick={() => onPage("Accounts")}
            >
              <span className="account-mini-icon">
                <Landmark size={17} />
              </span>
              <span>
                <strong>{a.subtype || a.type}</strong>
                <small>
                  {a.institution} · {a.mask}
                </small>
              </span>
              <b>{money(a.balance, true)}</b>
            </button>
          ))}
          {!state.accounts.length && (
            <button className="text-button" onClick={onLink}>
              connect your first account
              <Plus size={14} />
            </button>
          )}
          <button
            className="dashboard-section-link"
            onClick={() => onPage("Accounts")}
          >
            all accounts
            <ChevronRight size={15} />
          </button>
        </section>
        <section className="dashboard-card upcoming-card">
          <div className="dashboard-card-heading">
            <h2>upcoming</h2>
            <Repeat2 size={16} />
          </div>
          {upcoming.map((r) => {
            const date = new Date(r.nextDate + "T12:00:00");
            return (
              <button
                className="upcoming-row"
                key={r.id}
                onClick={() => onPage("Recurring")}
              >
                <span className="bill-date">
                  <small>
                    {date.toLocaleDateString("en-US", { month: "short" })}
                  </small>
                  <b>{date.getDate()}</b>
                </span>
                <span>{r.merchant}</span>
                <strong>{money(r.amount, true)}</strong>
              </button>
            );
          })}
          {!upcoming.length && (
            <p className="dashboard-empty">no confirmed bills coming up.</p>
          )}
          <button
            className="dashboard-section-link"
            onClick={() => onPage("Recurring")}
          >
            all recurring
            <ChevronRight size={15} />
          </button>
        </section>
        <section className="dashboard-card dashboard-goal">
          <div className="dashboard-card-heading">
            <h2>savings goal</h2>
            <Sprout size={17} />
          </div>
          <button className="goal-open" onClick={() => onPage("Savings goal")}>
            <span>{state.goal.name}</span>
            <ChevronRight size={15} />
          </button>
          <div className="goal-mini-value">
            <strong>{money(state.goal.saved)}</strong>
            <span> / {money(state.goal.target)}</span>
          </div>
          <div className="progress-track">
            <i
              style={{
                width: `${Math.min(100, (state.goal.saved / Math.max(1, state.goal.target)) * 100)}%`,
              }}
            />
          </div>
          <p>{money(state.goal.monthly)} set aside each month</p>
        </section>
      </aside>
    </div>
  );
}
