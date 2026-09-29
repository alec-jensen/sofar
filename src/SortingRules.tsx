import { useEffect, useState } from "react";
import { ArrowLeft, Plus, X } from "lucide-react";
import { normalize, type Action, type Category, type State } from "./model";
import { useSheetFocus } from "./useSheetFocus";
import { useSheetDrag } from "./useSheetDrag";

type Draft = { pattern: string; oldPattern?: string; category: Category; subcategoryId?: string };
const groups: Category[] = ["expenses", "spending", "savings"];

export default function SortingRules({ state, onBack, onAction }: { state: State; onBack: () => void; onAction: (action: Omit<Action, "id">, message?: string) => Promise<boolean> }) {
  const [draft, setDraft] = useState<Draft | null>(null);
  const [deleteStep, setDeleteStep] = useState(false);
  const [saving, setSaving] = useState(false);
  const [applyPast, setApplyPast] = useState(false);
  const suggestions = (() => {
    const groups = new Map<string, { merchant: string; category: Category; subcategoryId?: string; count: number }>();
    for (const t of state.transactions) {
      if (t.status !== "confirmed" || !t.category || t.ignored || t.splits) continue;
      const pattern = normalize(t.merchant);
      if (pattern.length < 2 || state.rules.some(r => r.pattern === pattern)) continue;
      const existing = groups.get(pattern);
      if (existing?.count === -1) continue;
      if (existing && (existing.category !== t.category || existing.subcategoryId !== t.subcategoryId)) { existing.count = -1; continue; }
      groups.set(pattern, { merchant: t.merchant, category: t.category, subcategoryId: t.subcategoryId, count: (existing?.count || 0) + 1 });
    }
    return Array.from(groups.entries())
      .filter(([, g]) => g.count >= 3)
      .slice(0, 3)
      .map(([pattern, g]) => ({ pattern, ...g }));
  })();
  const sheet = useSheetDrag(!!draft, () => { setDraft(null); setDeleteStep(false); });
  useSheetFocus(!!draft);
  useEffect(() => {
    if (!draft) return;
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") sheet.close(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [draft]);
  const edit = (rule?: State["rules"][number]) => {
    setDeleteStep(false);
    setApplyPast(false);
    setDraft(rule ? { pattern: rule.pattern, oldPattern: rule.pattern, category: rule.category, subcategoryId: rule.subcategoryId } : { pattern: "", category: "spending" });
  };
  const save = async () => {
    if (!draft || normalize(draft.pattern).length < 2) return;
    setSaving(true);
    try {
      const pattern = normalize(draft.pattern);
      if (await onAction({ type: "rule-upsert", pattern, oldPattern: draft.oldPattern, category: draft.category, subcategoryId: draft.subcategoryId }, "Sorting rule saved.")) {
        if (applyPast) await onAction({ type: "rule-backfill", pattern, category: draft.category, subcategoryId: draft.subcategoryId }, "Past matches updated too.");
        sheet.close();
      }
    }
    finally { setSaving(false); }
  };
  const remove = async () => {
    if (!draft?.oldPattern) return;
    setSaving(true);
    try { if (await onAction({ type: "rule-delete", pattern: draft.oldPattern }, "Sorting rule deleted.")) sheet.close(); }
    finally { setSaving(false); }
  };
  const draftMatches = draft && normalize(draft.pattern).length >= 2
    ? state.transactions.filter(t => normalize(t.merchant).includes(normalize(draft.pattern)))
    : [];
  return <div className="design-setup">
    <div className="design-setup-heading"><button onClick={onBack}><ArrowLeft size={16} /> tools</button><span>/</span><h1>sorting rules</h1><button className="design-setup-new" data-h="sheet" onClick={() => edit()}><Plus size={16} /> new</button></div>
    <p className="design-setup-intro">new transactions check the most specific merchant matches first. anything left over, we guess and you review.</p>
    {suggestions.length > 0 && (
      <div className="design-suggestions">
        {suggestions.map(sg => {
          const target = (state.subcategories || []).find(c => c.id === sg.subcategoryId)?.name || state.categories[sg.category];
          return (
            <div className="design-suggestion" key={sg.pattern}>
              <span>you’ve moved <strong>{sg.merchant.toLowerCase()}</strong> to <strong>{target}</strong> {sg.count} times.</span>
              <button data-h="tap" onClick={() => onAction({ type: "rule-upsert", pattern: sg.pattern, category: sg.category, subcategoryId: sg.subcategoryId }, "Sorting rule saved.")}>make it a rule</button>
            </div>
          );
        })}
      </div>
    )}
    <div className="design-rules-list">{state.rules.length ? state.rules.map(rule => {
      const matches = state.transactions.filter(t => normalize(t.merchant).includes(rule.pattern));
      const target = (state.subcategories || []).find(c => c.id === rule.subcategoryId)?.name || state.categories[rule.category];
      return <div className="design-rule" key={rule.pattern}>
        <button className="design-rule-main" data-h="sheet" onClick={() => edit(rule)}><span>when merchant has “{rule.pattern}”</span><strong>→ {target}</strong><small>{matches.length} {matches.length === 1 ? "match" : "matches"}{matches.length ? ` · last ${new Date(matches.map(t => t.date).sort().at(-1)! + "T12:00:00").toLocaleDateString("en-US", { month: "short", day: "numeric" }).toLowerCase()}` : ""}</small></button>
        <button className={`design-rule-switch ${rule.enabled === false ? "" : "on"}`} data-h={rule.enabled === false ? "toggle" : "toggleOff"} role="switch" aria-checked={rule.enabled !== false} aria-label={`${rule.enabled === false ? "enable" : "disable"} rule for ${rule.pattern}`} onClick={() => onAction({ type: "rule-toggle", pattern: rule.pattern, enabled: rule.enabled === false }, rule.enabled === false ? "Rule enabled." : "Rule paused.")}><span /></button>
      </div>;
    }) : <p className="design-category-empty">no rules yet. make one for a merchant you recognize, or confirm a transaction to teach sofar.</p>}</div>
    <div className="design-rules-fallback"><span>everything else</span><strong>→ we guess, you review</strong></div>
    {draft && <div className="design-sheet-backdrop" data-closing={sheet.closing} onClick={sheet.close}><div className="design-sheet" role="dialog" aria-modal="true" aria-label={draft.oldPattern ? "edit sorting rule" : "new sorting rule"} data-dragging={sheet.dragging} style={sheet.style} {...sheet.handlers} onClick={event => event.stopPropagation()}>
      <div className="history-sheet-handle" />
      <div className="design-sheet-title"><h2>{draft.oldPattern ? "edit rule" : "new rule"}</h2><button aria-label="close" data-h="sheetClose" onClick={sheet.close}><X size={20} /></button></div>
      <label className="design-sheet-field">merchant has<input value={draft.pattern} maxLength={80} onChange={event => setDraft({ ...draft, pattern: event.target.value })} placeholder="e.g. trader joe" autoFocus /></label>
      <span className="design-sheet-label">sort into</span><div className="design-sheet-chips">{groups.map(group => <button key={group} data-h="tick" className={draft.category === group ? "active" : ""} onClick={() => setDraft({ ...draft, category: group, subcategoryId: undefined })}>{state.categories[group]}</button>)}</div>
      {(state.subcategories || []).some(c => c.group === draft.category) && <label className="design-sheet-field">category<select value={draft.subcategoryId || ""} onChange={event => setDraft({ ...draft, subcategoryId: event.target.value || undefined })}><option value="">group only</option>{(state.subcategories || []).filter(c => c.group === draft.category).map(c => <option key={c.id} value={c.id}>{c.name}</option>)}</select></label>}
      <p className="design-sheet-help">matches a merchant’s name, ignoring punctuation and capitalization. you’ll still review new transactions.</p>
      <div className="design-rule-preview"><strong>{draftMatches.length} {draftMatches.length === 1 ? "past match" : "past matches"}</strong>{draftMatches.slice(0, 2).map(t => <span key={t.id}><span>{t.merchant.toLowerCase()}</span><span>{new Date(t.date + "T12:00:00").toLocaleDateString("en-US", { month: "short", day: "numeric" }).toLowerCase()} · {Math.abs(t.amount / 100).toLocaleString("en-US", { style: "currency", currency: "USD" })}</span></span>)}</div>
      {draftMatches.length > 0 && (
        <label className="account-toggle">
          <span>fix past ones too</span>
          <button type="button" data-h="toggle" className={`design-rule-switch ${applyPast ? "on" : ""}`} onClick={() => setApplyPast(!applyPast)}><span /></button>
        </label>
      )}
      <button className="design-sheet-save" data-h="success" disabled={saving || normalize(draft.pattern).length < 2} onClick={save}>{saving ? "saving…" : "save rule"}</button>
      {draft.oldPattern && <div className="design-delete-area">{deleteStep ? <><p>delete this rule? new matches will need review again.</p><div><button onClick={() => setDeleteStep(false)}>keep it</button><button data-h="thud" disabled={saving} onClick={remove}>yes, delete</button></div></> : <button data-h="soft" onClick={() => setDeleteStep(true)}>delete rule</button>}</div>}
    </div></div>}
  </div>;
}
