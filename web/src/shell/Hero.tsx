import { useEffect, useState } from "react";
import RollingNumber from "../RollingNumber";
import { api } from "./api";
import { dollars } from "./format";

type Home = {
  month: string;
  spend_left: number;
  per_day: number;
  last_day_extra: number;
  learning: boolean;
  partial: boolean;
  overcommitted: number;
  uncategorized: number;
  stale: boolean;
  data_as_of: string | null;
  unreviewed_count: number;
  has_data: boolean;
};

type Explain = { sentences: string[]; spend_left_lines: { label: string; amount: number }[] };

// The hero: the server's safe-to-spend number, with no client-side math.
export default function Hero() {
  const [home, setHome] = useState<Home | null>(null),
    [error, setError] = useState(""),
    [why, setWhy] = useState<Explain | null>(null),
    [showWhy, setShowWhy] = useState(false);

  useEffect(() => {
    api<Home>("/home")
      .then(setHome)
      .catch((e) => setError((e as Error).message));
  }, []);

  useEffect(() => {
    if (!showWhy || !home || why) return;
    api<Explain>(`/months/${home.month}/explain`)
      .then(setWhy)
      .catch(() => setWhy({ sentences: ["there is nothing to explain yet."], spend_left_lines: [] }));
  }, [showWhy, home, why]);

  if (error)
    return (
      <section className="card shell-card" role="alert">
        <p>{error}</p>
      </section>
    );
  if (!home)
    return (
      <section className="card shell-card" aria-busy="true">
        <p>working out your number…</p>
      </section>
    );

  const quiet = !home.has_data
    ? "add some transactions and your number appears here."
    : home.learning
      ? "sofar plans from your last full month of income, so your number appears once a full month of paychecks is in. mark your income in review."
      : "";
  const asOf = home.data_as_of ? new Date(home.data_as_of).toLocaleString() : "";

  return (
    <section className="reference-hero shell-hero" aria-label="safe to spend">
      <h1 className="reference-hero-label">safe to spend this month</h1>
      <strong className="reference-hero-number">
        {quiet ? "—" : <RollingNumber value={home.spend_left} format={(v) => dollars(Math.round(v))} />}
      </strong>
      <p className="reference-hero-sub">
        {quiet ||
          (home.spend_left < 0
            ? "you are over by " + dollars(-home.spend_left) + " this month"
            : "about " + dollars(home.per_day) + " a day")}
      </p>
      {!quiet && home.uncategorized > 0 && <p className="reference-hero-sub">{dollars(home.uncategorized)} uncategorized, waiting for you</p>}
      {!quiet && home.overcommitted > 0 && <p className="reference-hero-sub">fixed costs are {dollars(home.overcommitted)} over the plan</p>}
      {home.partial && <p className="reference-hero-sub">some accounts are missing earlier data, so this plan is partial.</p>}
      {home.unreviewed_count > 0 && <p className="reference-hero-sub">{home.unreviewed_count} to review</p>}
      {home.stale && asOf && <p className="reference-hero-sub">data as of {asOf}</p>}
      {!quiet && (
        <button className="text-button" onClick={() => setShowWhy((v) => !v)} aria-expanded={showWhy}>
          {showWhy ? "hide" : "why this number"}
        </button>
      )}
      {showWhy && why && (
        <div className="shell-why">
          <ul>
            {why.sentences.map((s) => (
              <li key={s}>{s}</li>
            ))}
          </ul>
          <ul className="shell-why-lines">
            {why.spend_left_lines.map((l) => (
              <li key={l.label}>
                <span>{l.label}</span>
                <b>{dollars(l.amount)}</b>
              </li>
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}
