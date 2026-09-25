import { useEffect, useState } from "react";
import { ArrowLeft, Plus, X } from "lucide-react";
import { budget, money, type Action, type Category, type State, type Subcategory } from "./model";
import { useSheetFocus } from "./useSheetFocus";
import { useSheetDrag } from "./useSheetDrag";

const groups: Category[] = ["expenses", "spending", "savings"];

export default function Categories({ state, onBack, onAction }: { state: State; onBack: () => void; onAction: (action: Omit<Action, "id">, message?: string) => Promise<boolean> }) {
  const [edit, setEdit] = useState<Subcategory | null>(null);
  const [deleteStep, setDeleteStep] = useState(false);
  const [saving, setSaving] = useState(false);
  const sheet = useSheetDrag(!!edit, () => { setEdit(null); setDeleteStep(false); });
  useSheetFocus(!!edit);
  const b = budget(state);
  const month = `${new Date().getFullYear()}-${String(new Date().getMonth() + 1).padStart(2, "0")}`;
  const subcategories = state.subcategories || [];
  useEffect(() => {
    if (!edit) return;
    const onKey = (event: KeyboardEvent) => { if (event.key === "Escape") sheet.close(); };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [edit]);
  const spent = (id: string) => state.transactions
    .filter(t => t.subcategoryId === id && t.status === "confirmed" && t.direction === "out" && t.date.startsWith(month))
    .reduce((sum, t) => sum + t.amount - state.links.filter(link => link.expenseId === t.id).reduce((n, link) => n + link.amount, 0), 0);
  const startEdit = (category?: Subcategory) => {
    setDeleteStep(false);
    setEdit(category ? { ...category } : { id: crypto.randomUUID(), name: "", group: "spending", monthlyPlan: 0 });
  };
  const save = async () => {
    if (!edit || !edit.name.trim() || !Number.isFinite(edit.monthlyPlan) || edit.monthlyPlan < 0) return;
    setSaving(true);
    try { if (await onAction({ type: "subcategory-upsert", subcategory: { ...edit, name: edit.name.trim().toLowerCase() } }, "Category saved.")) sheet.close(); }
    finally { setSaving(false); }
  };
  const remove = async () => {
    if (!edit) return;
    setSaving(true);
    try { if (await onAction({ type: "subcategory-delete", subcategoryId: edit.id }, "Category deleted. Transactions remain in their group.")) sheet.close(); }
    finally { setSaving(false); }
  };
  return <div className="design-setup">
    <div className="design-setup-heading"><button onClick={onBack}><ArrowLeft size={16} /> tools</button><span>/</span><h1>categories</h1><button className="design-setup-new" data-h="sheet" onClick={() => startEdit()}><Plus size={16} /> new</button></div>
    <p className="design-setup-intro">three groups. expenses get set aside first, spending is what safe to spend covers, savings is money you’re keeping.</p>
    {groups.map(group => {
      const items = subcategories.filter(c => c.group === group);
      const planned = items.reduce((sum, c) => sum + c.monthlyPlan, 0);
      return <section className="design-category-group" key={group}>
        <div className="design-category-group-heading"><h2>{state.categories[group]}</h2><span>{money(group === "expenses" ? Math.max(planned, b.bills) : group === "savings" ? Math.max(planned, state.goal.monthly) : planned)}/mo</span></div>
        <div className="design-category-list">
          {items.length ? items.map(c => {
            const total = spent(c.id);
            return <button key={c.id} className="design-category-row" data-h="sheet" onClick={() => startEdit(c)}>
              <span><strong>{c.name}</strong><small>{money(total)} of {c.monthlyPlan ? money(c.monthlyPlan) : "no plan"}</small></span>
              {c.monthlyPlan > 0 && <span className="design-category-progress"><i style={{ width: `${Math.min(100, total / c.monthlyPlan * 100)}%` }} /></span>}
              <small>{c.monthlyPlan ? total <= c.monthlyPlan ? `${money(c.monthlyPlan - total)} left` : `${money(total - c.monthlyPlan)} over` : "tap to set a plan"}</small>
            </button>;
          }) : <p className="design-category-empty">nothing here yet. add a category to plan this group.</p>}
        </div>
      </section>;
    })}
    {edit && <div className="design-sheet-backdrop" data-closing={sheet.closing} onClick={sheet.close}><div className="design-sheet" role="dialog" aria-modal="true" aria-label={edit.name ? `edit ${edit.name}` : "new category"} data-dragging={sheet.dragging} style={sheet.style} {...sheet.handlers} onClick={event => event.stopPropagation()}>
      <div className="history-sheet-handle" />
      <div className="design-sheet-title"><h2>{subcategories.some(c => c.id === edit.id) ? "edit category" : "new category"}</h2><button aria-label="close" data-h="sheetClose" onClick={sheet.close}><X size={20} /></button></div>
      <label className="design-sheet-field">name<input maxLength={60} value={edit.name} onChange={event => setEdit({ ...edit, name: event.target.value })} placeholder="e.g. groceries" autoFocus /></label>
      <span className="design-sheet-label">group</span><div className="design-sheet-chips">{groups.map(group => <button key={group} data-h="tick" className={edit.group === group ? "active" : ""} onClick={() => setEdit({ ...edit, group })}>{state.categories[group]}</button>)}</div>
      <label className="design-sheet-field">monthly plan<input type="number" inputMode="decimal" min="0" max="100000000" step="0.01" value={edit.monthlyPlan / 100} onChange={event => setEdit({ ...edit, monthlyPlan: Math.max(0, Math.round((Number(event.target.value) || 0) * 100)) })} /></label>
      <p className="design-sheet-help">moving a category also moves its assigned transactions and sorting rules to that group.</p>
      <button className="design-sheet-save" data-h="success" disabled={saving || !edit.name.trim()} onClick={save}>{saving ? "saving…" : "save category"}</button>
      {subcategories.some(c => c.id === edit.id) && <div className="design-delete-area">{deleteStep ? <><p>delete “{edit.name}”? transactions will keep their group, but lose this category. this can’t be undone.</p><div><button onClick={() => setDeleteStep(false)}>keep it</button><button data-h="thud" disabled={saving} onClick={remove}>yes, delete</button></div></> : <button data-h="soft" onClick={() => setDeleteStep(true)}>delete category</button>}</div>}
    </div></div>}
  </div>;
}
