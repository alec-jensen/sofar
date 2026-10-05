import { useState } from "react";
import { ArrowRight, Check, Landmark, Repeat2, ShieldCheck, Sprout } from "lucide-react";
import { money, normalize, type Action, type State } from "./model";

type Props = {
  state: State;
  onAction: (action: Omit<Action, "id">, message?: string) => Promise<boolean>;
  onLinkBank: () => void;
  onDone: () => void;
};

type IncomeChoice = "salary" | "self-employed" | "transfer" | "other";
const steps = ["connect", "payday", "bills", "goal", "done"] as const;
type Step = (typeof steps)[number];

export default function Onboarding({ state, onAction, onLinkBank, onDone }: Props) {
  const [step, setStep] = useState<Step>("connect");
  const index = steps.indexOf(step);
  const next = () => setStep(steps[Math.min(steps.length - 1, index + 1)]);
  // Incoming money grouped by who sent it, so each payer is sorted once.
  const incomeGroups = (() => {
    const linked = new Set(state.links.map((l) => l.creditId));
    const groups = new Map<string, { key: string; merchant: string; count: number; total: number; stream?: IncomeChoice; pending: typeof state.transactions }>();
    for (const t of state.transactions) {
      if (t.direction !== "in" || t.bankPending || t.ignored || linked.has(t.id)) continue;
      if (t.status === "confirmed" && !t.incomeStream) continue;
      const key = normalize(t.merchant) || t.merchant;
      const g = groups.get(key) || { key, merchant: t.merchant, count: 0, total: 0, pending: [] };
      g.count += 1;
      g.total += t.amount;
      if (t.status === "pending") g.pending.push(t);
      else if (t.incomeStream) g.stream = t.incomeStream;
      groups.set(key, g);
    }
    return Array.from(groups.values()).sort((a, b) => b.total - a.total).slice(0, 8);
  })();
  const [incomeChoice, setIncomeChoice] = useState<Record<string, IncomeChoice | undefined>>(
    () => Object.fromEntries(incomeGroups.map((g) => [g.key, g.stream || g.pending[0]?.suggestedIncomeStream])),
  );
  const [saving, setSaving] = useState(false);
  const bills = state.recurring.filter((r) => r.type === "bill" && !r.confirmed);
  const [checkedBills, setCheckedBills] = useState<Set<string>>(new Set(bills.map((b) => b.id)));
  const [goalName, setGoalName] = useState(state.goal.name || "a little breathing room");
  const [goalMonthly, setGoalMonthly] = useState(state.goal.monthly);

  return (
    <div className="onboarding-shell">
      <div className="onboarding-card">
        <div className="onboarding-progress">
          {steps.map((s, i) => (
            <span key={s} data-done={i <= index} />
          ))}
        </div>
        {step === "connect" && (
          <>
            <span className="modal-symbol"><Landmark /></span>
            <h1>let's connect your accounts</h1>
            <p>sofar reads your transactions through simplefin bridge to build your safe-to-spend number. you’ll need a simplefin setup token.</p>
            {state.accounts.length > 0 && (
              <div className="onboarding-status">
                <ShieldCheck size={16} />
                {state.accounts.length} account{state.accounts.length === 1 ? "" : "s"} connected
              </div>
            )}
            <button className="button primary full" onClick={onLinkBank}>
              {state.connection?.connected ? "use a different token" : "connect with simplefin"}
              <ArrowRight size={17} />
            </button>
          </>
        )}
        {step === "payday" && (
          <>
            <span className="modal-symbol"><Repeat2 /></span>
            <h1>which of these are income?</h1>
            <p>your safe-to-spend number is built from money you actually received. tell sofar what each deposit is.</p>
            {incomeGroups.length ? (
              <div className="onboarding-list">
                {incomeGroups.map((g) => (
                  <div className="onboarding-row onboarding-income" key={g.key}>
                    <span><strong>{g.merchant.toLowerCase()}</strong><small>{g.count} deposit{g.count === 1 ? "" : "s"} · {money(g.total)} total</small></span>
                    <div className="design-sheet-chips" role="radiogroup" aria-label={`what is ${g.merchant}?`}>
                      {([["salary", "paycheck"], ["self-employed", "self-employed"], ["other", "other income"], ["transfer", "not income"]] as const).map(([value, label]) => (
                        <button key={value} type="button" role="radio" aria-checked={incomeChoice[g.key] === value} data-h="tick" className={incomeChoice[g.key] === value ? "active" : ""} onClick={() => setIncomeChoice({ ...incomeChoice, [g.key]: value })}>{label}</button>
                      ))}
                    </div>
                  </div>
                ))}
              </div>
            ) : (
              <p className="footnote">no deposits yet. once money comes in, you'll sort it in review.</p>
            )}
          </>
        )}
        {step === "bills" && (
          <>
            <span className="modal-symbol"><Repeat2 /></span>
            <h1>which of these are regular bills?</h1>
            <p>checked bills are set aside before your safe-to-spend number. unchecked ones stop being suggested; you can bring them back from recurring.</p>
            {bills.length ? (
              <div className="onboarding-list">
                {bills.map((r) => (
                  <label className="onboarding-row" key={r.id}>
                    <span><strong>{r.merchant.toLowerCase()}</strong><small>{r.cadence} · {money(r.amount)}</small></span>
                    <input
                      type="checkbox"
                      checked={checkedBills.has(r.id)}
                      onChange={() => {
                        const next = new Set(checkedBills);
                        if (next.has(r.id)) next.delete(r.id);
                        else next.add(r.id);
                        setCheckedBills(next);
                      }}
                    />
                  </label>
                ))}
              </div>
            ) : (
              <p className="footnote">no bill patterns detected yet. sofar will suggest them as they repeat — you can confirm them anytime from recurring.</p>
            )}
          </>
        )}
        {step === "goal" && (
          <>
            <span className="modal-symbol"><Sprout /></span>
            <h1>set a savings goal</h1>
            <p>a little set aside today. more room tomorrow. you can change this anytime.</p>
            <label className="field">
              goal name
              <input value={goalName} onChange={(e) => setGoalName(e.target.value)} maxLength={80} />
            </label>
            <span className="design-sheet-label">monthly contribution</span>
            <div className="design-sheet-chips">
              {[2500, 5000, 10000, 20000].map((amount) => (
                <button key={amount} data-h="tick" className={goalMonthly === amount ? "active" : ""} onClick={() => setGoalMonthly(amount)}>{money(amount)}</button>
              ))}
            </div>
          </>
        )}
        {step === "done" && (
          <>
            <span className="modal-symbol"><Check /></span>
            <h1>you're set.</h1>
            <p>sofar's already working out your safe-to-spend number. a little clarity, right where you left off.</p>
          </>
        )}
        <div className="onboarding-actions">
          {step !== "done" && (
            <button
              className="text-button"
              disabled={saving}
              onClick={() => {
                if (step === "bills") {
                  (async () => {
                    setSaving(true);
                    try {
                      for (const r of bills) {
                        if (checkedBills.has(r.id)) await onAction({ type: "recurring", recurringId: r.id, category: r.category, tolerance: r.tolerance, amount: r.amount }, "Bills saved.");
                        else await onAction({ type: "dismiss-recurring", recurringId: r.id }, "Bills saved.");
                      }
                      next();
                    } finally { setSaving(false); }
                  })();
                } else if (step === "payday") {
                  (async () => {
                    setSaving(true);
                    try {
                      for (const group of incomeGroups) {
                        const choice = incomeChoice[group.key];
                        if (!choice) continue;
                        for (const t of group.pending) await onAction({ type: "review", transactionId: t.id, category: t.suggested || "spending", incomeStream: choice }, "Income sorted.");
                      }
                      next();
                    } finally { setSaving(false); }
                  })();
                } else if (step === "goal") {
                  onAction({ type: "goal", goal: { ...state.goal, name: goalName.trim() || state.goal.name, monthly: goalMonthly } }, "Savings goal set.").then(next);
                } else next();
              }}
            >
              {saving ? "saving…" : step === "connect" ? (state.accounts.length ? "continue" : "skip for now") : step === "bills" && bills.length ? "confirm & continue" : "continue"}
              <ArrowRight size={16} />
            </button>
          )}
          {step === "done" && (
            <button className="button primary full" data-h="success" onClick={onDone}>
              let's go
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
