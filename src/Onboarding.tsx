import { useState } from "react";
import { ArrowRight, Check, Landmark, Repeat2, ShieldCheck, Sprout } from "lucide-react";
import { money, normalize, type Action, type State } from "./model";

type Props = {
  state: State;
  onAction: (action: Omit<Action, "id">, message?: string) => Promise<boolean>;
  onLinkBank: () => void;
  onDone: () => void;
};

const steps = ["connect", "payday", "bills", "goal", "done"] as const;
type Step = (typeof steps)[number];

export default function Onboarding({ state, onAction, onLinkBank, onDone }: Props) {
  const [step, setStep] = useState<Step>("connect");
  const index = steps.indexOf(step);
  const next = () => setStep(steps[Math.min(steps.length - 1, index + 1)]);
  const paydays = (() => {
    const groups = new Map<string, { merchant: string; count: number; total: number }>();
    for (const t of state.transactions) {
      if (t.status !== "confirmed" || t.direction !== "in" || !t.incomeStream || t.incomeStream === "transfer") continue;
      const key = normalize(t.merchant);
      const g = groups.get(key) || { merchant: t.merchant, count: 0, total: 0 };
      g.count += 1;
      g.total += t.amount;
      groups.set(key, g);
    }
    return Array.from(groups.values()).sort((a, b) => b.total - a.total);
  })();
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
            <p>sofar reads your transactions to build your safe-to-spend number. banking, savings, or investments.</p>
            {state.accounts.length > 0 && (
              <div className="onboarding-status">
                <ShieldCheck size={16} />
                {state.accounts.length} account{state.accounts.length === 1 ? "" : "s"} connected
              </div>
            )}
            <button className="button primary full" onClick={onLinkBank}>
              connect an account
              <ArrowRight size={17} />
            </button>
          </>
        )}
        {step === "payday" && (
          <>
            <span className="modal-symbol"><Repeat2 /></span>
            <h1>here's what looks like income</h1>
            <p>regular deposits sofar has spotted in your recent transactions.</p>
            {paydays.length ? (
              <div className="onboarding-list">
                {paydays.map((p) => (
                  <div className="onboarding-row confirmed" key={p.merchant}>
                    <span><strong>{p.merchant.toLowerCase()}</strong><small>{p.count} deposit{p.count === 1 ? "" : "s"} · {money(p.total)} total</small></span>
                    <Check size={18} />
                  </div>
                ))}
              </div>
            ) : (
              <p className="footnote">nothing detected yet. once income comes in and you confirm it during review, it'll show up here.</p>
            )}
          </>
        )}
        {step === "bills" && (
          <>
            <span className="modal-symbol"><Repeat2 /></span>
            <h1>which of these are regular bills?</h1>
            <p>we set these aside first, before your safe-to-spend number.</p>
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
              onClick={() => {
                if (step === "bills") {
                  Promise.all(
                    Array.from(checkedBills).map((id) => {
                      const r = bills.find((b) => b.id === id);
                      return r ? onAction({ type: "recurring", recurringId: id, category: r.category }, "Bill confirmed.") : Promise.resolve(true);
                    }),
                  ).then(next);
                } else if (step === "goal") {
                  onAction({ type: "goal", goal: { ...state.goal, name: goalName.trim() || state.goal.name, monthly: goalMonthly } }, "Savings goal set.").then(next);
                } else next();
              }}
            >
              {step === "connect" ? "skip for now" : step === "bills" && bills.length ? "confirm & continue" : "continue"}
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
