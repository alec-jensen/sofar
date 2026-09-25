import Dashboard from "./Dashboard";
import ShouldIBuy from "./ShouldIBuy";
import Calculators from "./Calculators";
import Categories from "./Categories";
import SortingRules from "./SortingRules";
import { configureMotion, emitHaptic, installFeedback, playPageEntrance, setHapticEnabled, type MotionMode } from "./motion";
import { useSwipeCard } from "./useSwipeCard";
import { useSheetDrag } from "./useSheetDrag";
import { useSheetFocus } from "./useSheetFocus";
import {
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react";
import {
  ArrowDownLeft,
  ArrowLeft,
  ArrowRight,
  ArrowUpRight,
  Bell,
  BookOpen,
  Calculator,
  Check,
  CheckCheck,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  CloudOff,
  Download,
  Home,
  Inbox,
  Landmark,
  Leaf,
  Link2,
  Loader2,
  LogOut,
  MoreHorizontal,
  Plus,
  RefreshCw,
  Repeat2,
  Search,
  Settings2,
  ShieldCheck,
  ShoppingBag,
  Sparkles,
  Sprout,
  Wallet,
  X,
} from "lucide-react";
import {
  budget,
  demoState,
  money,
  reimbursementMatches,
  type Action,
  type Category,
  type Recurring,
  type State,
  type Transaction,
} from "./model";
import {
  api,
  cached,
  clearPrivate,
  dispatch,
  flush,
  loadDemo,
  save,
} from "./store";
type Page =
  | "Overview"
  | "Should I buy this"
  | "Calculators"
  | "Categories"
  | "Sorting rules"
  | "Transactions"
  | "Review inbox"
  | "Recurring"
  | "Savings goal"
  | "Accounts"
  | "Settings";
const categories: Category[] = ["expenses", "spending", "savings"];
const pageHashes: Record<Page, string> = {
  Overview: "#overview",
  "Should I buy this": "#buy",
  Calculators: "#calculators",
  Categories: "#categories",
  "Sorting rules": "#rules",
  Transactions: "#transactions",
  "Review inbox": "#review",
  Recurring: "#recurring",
  "Savings goal": "#savings",
  Accounts: "#accounts",
  Settings: "#settings",
};
const pageFromHash = (): Page =>
  (Object.entries(pageHashes).find(([, hash]) => hash === location.hash)?.[0] as
    | Page
    | undefined) || "Overview";
const icons = {
  Overview: Home,
  "Should I buy this": ShoppingBag,
  Calculators: Calculator,
  Categories: Wallet,
  "Sorting rules": Settings2,
  Transactions: ArrowDownLeft,
  "Review inbox": Inbox,
  Recurring: Repeat2,
  "Savings goal": Sprout,
  Accounts: Landmark,
  Settings: Settings2,
};
const dateLabel = (date: string) =>
  new Date(date + "T12:00:00").toLocaleDateString("en-US", {
    month: "short",
    day: "numeric",
  });
function Mark({ small = false }: { small?: boolean }) {
  return (
    <span className={`brand-mark ${small ? "small" : ""}`}>
      <span />
      <span />
      <span />
    </span>
  );
}
function Merchant({ name }: { name: string }) {
  return (
    <span
      aria-hidden="true"
      className={`merchant-icon ${name.toLowerCase().includes("spotify") ? "spotify" : name.toLowerCase().includes("netflix") ? "netflix" : ""}`}
    >
      {name.includes("Coffee") ? (
        "☕"
      ) : name.includes("Rent") ? (
        <Home size={18} />
      ) : name.includes("fund") ? (
        <Sprout size={19} />
      ) : name.toLowerCase().includes("spotify") ? (
        "≋"
      ) : (
        name.charAt(0)
      )}
    </span>
  );
}
export default function App() {
  const [s, setState] = useState<State | null>(null),
    [auth, setAuth] = useState<"loading" | "login" | "setup" | "ready">(
      "loading",
    );
  const [page, setPage] = useState<Page>(pageFromHash),
    [notice, setNotice] = useState(""),
    [modal, setModal] = useState<ReactNode>(null),
    [busy, setBusy] = useState(false),
    [offline, setOffline] = useState(!navigator.onLine),
    [compact, setCompact] = useState(window.innerWidth <= 700),
    [monthOffset, setMonthOffset] = useState(0),
    [pushEnabled, setPushEnabled] = useState(false),
    [totpEnabled, setTotpEnabled] = useState(false),
    [hapticsEnabled, setHapticsEnabled] = useState(() => localStorage.getItem("sofar-haptics") !== "off"),
    [motionMode, setMotionMode] = useState<MotionMode>(() => {
      const saved = localStorage.getItem("sofar-motion");
      return saved === "calm" || saved === "off" ? saved : "expressive";
    });
  const [search, setSearch] = useState(""),
    [filter, setFilter] = useState("all"),
    [accountFilter, setAccountFilter] = useState("all"),
    [historyDate, setHistoryDate] = useState<"this month" | "all time">("this month"),
    [historyFiltersOpen, setHistoryFiltersOpen] = useState(false),
    [expandedTransaction, setExpandedTransaction] = useState<string | null>(null),
    [reviewCategory, setReviewCategory] = useState<Category | null>(null),
    [reviewSubcategoryId, setReviewSubcategoryId] = useState<string | undefined>(undefined),
    [stream, setStream] = useState("salary"),
    [reviewPicker, setReviewPicker] = useState(false),
    [reviewId, setReviewId] = useState("");
  const actionInFlight = useRef(false);
  const liveState = useRef(s);
  liveState.current = s;
  useEffect(() => installFeedback(), []);
  useEffect(() => { setHapticEnabled(hapticsEnabled); }, [hapticsEnabled]);
  useEffect(() => {
    configureMotion(motionMode);
    const media = window.matchMedia("(prefers-reduced-motion: reduce)");
    const update = () => configureMotion(motionMode);
    media.addEventListener("change", update);
    return () => media.removeEventListener("change", update);
  }, [motionMode]);
  useEffect(() => {
    if (auth !== "ready") return;
    const frame = requestAnimationFrame(() => playPageEntrance(page));
    return () => cancelAnimationFrame(frame);
  }, [auth, page]);
  useEffect(() => {
    const query = window.matchMedia("(max-width: 700px)");
    const update = () => setCompact(query.matches);
    query.addEventListener("change", update);
    return () => query.removeEventListener("change", update);
  }, []);
  useEffect(() => {
    const onHashChange = () => {
      setPage(pageFromHash());
      window.scrollTo(0, 0);
    };
    window.addEventListener("hashchange", onHashChange);
    return () => window.removeEventListener("hashchange", onHashChange);
  }, []);
  useEffect(() => {
    (async () => {
      try {
        const info = await api("/auth/status");
        if (info.authenticated) {
          setState(await flush());
          setTotpEnabled(!!info.totpEnabled);
          setAuth("ready");
        } else setAuth(info.setup ? "setup" : "login");
      } catch {
        const saved = await cached();
        if (saved) {
          setState(saved);
          setAuth("ready");
        } else {
          setState(await loadDemo());
          setAuth("ready");
        }
      }
    })();
  }, []);
  useEffect(() => {
    const online = () => {
      setOffline(false);
      if (liveState.current && !liveState.current.demo)
        flush()
          .then(setState)
          .catch((e) => setNotice(e.message));
    };
    const off = () => setOffline(true);
    window.addEventListener("online", online);
    window.addEventListener("offline", off);
    return () => {
      window.removeEventListener("online", online);
      window.removeEventListener("offline", off);
    };
  }, []);
  useEffect(() => {
    if (!notice) return;
    const t = setTimeout(() => setNotice(""), 5000);
    return () => clearTimeout(t);
  }, [notice]);
  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;
    const listener = (e: MessageEvent) => {
      if (e.data?.type === "open-review") go("Review inbox");
      if (
        e.data?.type === "flush-actions" &&
        liveState.current &&
        !liveState.current.demo
      )
        flush()
          .then(setState)
          .catch((e) => setNotice(e.message));
    };
    navigator.serviceWorker.addEventListener("message", listener);
    return () =>
      navigator.serviceWorker.removeEventListener("message", listener);
  }, []);
  useEffect(() => {
    if (!modal && !historyFiltersOpen && !reviewPicker) return;
    const before = document.activeElement as HTMLElement | null;
    const dialog = document.querySelector<HTMLElement>('[role="dialog"]');
    const selector =
      'button:not(:disabled),a[href],input:not(:disabled),select:not(:disabled),[tabindex="0"]';
    dialog?.querySelector<HTMLElement>(selector)?.focus();
    const trap = (e: KeyboardEvent) => {
      if (e.key !== "Tab") return;
      const items = Array.from(
        dialog?.querySelectorAll<HTMLElement>(selector) || [],
      );
      if (!items.length) return;
      const first = items[0],
        last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    };
    document.addEventListener("keydown", trap);
    return () => {
      document.removeEventListener("keydown", trap);
      before?.focus();
    };
  }, [modal, historyFiltersOpen, reviewPicker]);
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        setModal(null);
        setHistoryFiltersOpen(false);
        setReviewPicker(false);
      }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, []);
  useEffect(() => {
    if (s && "setAppBadge" in navigator)
      (navigator as Navigator & { setAppBadge: (n: number) => Promise<void> })
        .setAppBadge(
          s.transactions.filter((t) => t.status === "pending" && !t.bankPending)
            .length +
          s.recurring.filter((r) => !r.dismissed && (!r.confirmed || r.mismatch)).length,
        )
        .catch(() => {});
  }, [s]);
  useEffect(() => {
    if (!s || s.demo || !("serviceWorker" in navigator) || !("PushManager" in window))
      return;
    navigator.serviceWorker.ready
      .then((reg) => reg.pushManager.getSubscription())
      .then((sub) => setPushEnabled(!!sub))
      .catch(() => {});
  }, [s?.demo]);
  async function act(a: Omit<Action, "id">, message = "Saved.") {
    if (!s || actionInFlight.current) return false;
    actionInFlight.current = true;
    try {
      const next = await dispatch(s, a);
      setState(next);
      setNotice(
        offline && !s.demo
          ? "Saved on this device. We’ll sync when you’re back online."
          : message,
      );
      if (a.type !== "review") emitHaptic("success");
      setReviewCategory(null);
      setReviewSubcategoryId(undefined);
      setReviewPicker(false);
      setStream("salary");
      if (a.type === "review" && a.transactionId) {
        const queue = s.transactions.filter(
          (t) => t.status === "pending" && !t.bankPending,
        );
        const index = queue.findIndex((t) => t.id === a.transactionId);
        if (index >= 0)
          setReviewId(queue.length > 1
            ? queue[(index + 1) % queue.length].id
            : "");
      }
      return true;
    } catch (e) {
      setNotice((e as Error).message);
      emitHaptic("error");
      return false;
    } finally {
      actionInFlight.current = false;
    }
  }
  function go(p: Page) {
    if (location.hash !== pageHashes[p]) location.hash = pageHashes[p];
    setPage(p);
    setSearch("");
    setFilter("all");
    setAccountFilter("all");
    window.scrollTo(0, 0);
  }
  async function sync() {
    if (!s) return;
    setBusy(true);
    try {
      if (s.demo) {
        const next = { ...s, lastSync: new Date().toISOString() };
        await save(next);
        setState(next);
        setNotice(
          "Demo is up to date. Connect your banks in your own instance.",
        );
      } else {
        await api("/sync", {});
        setState(await flush());
        setNotice("Accounts synced. You’re up to date.");
      }
      emitHaptic("success");
    } catch (e) {
      setNotice((e as Error).message);
      emitHaptic("error");
    } finally {
      setBusy(false);
    }
  }
  async function notifications() {
    try {
      if (s?.demo) {
        setNotice(
          "Push notifications are available after signing in to your server.",
        );
        return;
      }
      if (!("Notification" in window) || !("PushManager" in window))
        throw new Error("This browser does not support push notifications.");
      if (pushEnabled) {
        const reg = await navigator.serviceWorker.ready;
        const sub = await reg.pushManager.getSubscription();
        if (sub) {
          await api("/push/unsubscribe", { endpoint: sub.endpoint });
          await sub.unsubscribe();
        }
        setPushEnabled(false);
        setNotice("Notifications are off for this device.");
        return;
      }
      const config = await api("/push/config");
      const permission = await Notification.requestPermission();
      if (permission !== "granted")
        throw new Error(
          "Notifications are off. You can allow them in browser settings.",
        );
      const reg = await navigator.serviceWorker.ready;
      const key = Uint8Array.from(
        atob(config.publicKey.replace(/-/g, "+").replace(/_/g, "/")),
        (c) => c.charCodeAt(0),
      );
      const sub =
        (await reg.pushManager.getSubscription()) ||
        (await reg.pushManager.subscribe({
          userVisibleOnly: true,
          applicationServerKey: key,
        }));
      await api("/push/subscribe", sub.toJSON());
      setPushEnabled(true);
      setNotice("Notifications are on for this device.");
    } catch (e) {
      setNotice((e as Error).message);
    }
  }
  async function linkBank(kind = "transactions") {
    if (s?.demo) {
      setModal(
        <div>
          <span className="modal-symbol">
            <Landmark />
          </span>
          <h2>connect an account</h2>
          <p>
            connect checking, savings, and investment accounts securely through
            plaid. this preview uses sample data.
          </p>
          <div className="info-box">
            <ShieldCheck size={20} />
            <span>
              start your go server and add your plaid keys to connect a real
              account. setup instructions are in the project readme.
            </span>
          </div>
          <button
            className="button primary full"
            onClick={() => setModal(null)}
          >
            got it
          </button>
        </div>,
      );
      return;
    }
    try {
      const { link_token } = await api("/plaid/link-token", { kind });
      if (!(window as any).Plaid) {
        await new Promise<void>((resolve, reject) => {
          const script = document.createElement("script");
          script.src =
            "https://cdn.plaid.com/link/v2/stable/link-initialize.js";
          script.onload = () => resolve();
          script.onerror = () =>
            reject(new Error("Could not load Plaid Link."));
          document.head.appendChild(script);
        });
      }
      (window as any).Plaid.create({
        token: link_token,
        onSuccess: async (public_token: string, metadata: any) => {
          try {
            await api("/plaid/exchange", {
              public_token,
              institution: metadata.institution?.name || "Your bank",
              kind,
            });
            await sync();
          } catch (e) {
            setNotice((e as Error).message);
          }
        },
        onExit: (err: any) => {
          if (err)
            setNotice(err.display_message || "Connection was not completed.");
        },
      }).open();
    } catch (e) {
      setNotice((e as Error).message);
    }
  }
  function editGoal() {
    if (!s) return;
    setModal(
      <GoalForm
        goal={s.goal}
        onSave={async (goal) => {
          if (await act({ type: "goal", goal }, "Your savings goal is updated."))
            setModal(null);
        }}
      />,
    );
  }
  function reimburse(t: Transaction, suggestedExpenseId = "") {
    if (!s) return;
    setModal(
      <ReimbursementForm
        state={s}
        credit={t}
        suggestedExpenseId={suggestedExpenseId}
        onSave={async (expenseId, amount) => {
          if (await act(
            { type: "reimburse", creditId: t.id, expenseId, amount },
            "Repayment linked. Your expense now reflects the net cost.",
          )) setModal(null);
        }}
      />,
    );
  }
  function recurringEdit(r: Recurring) {
    setModal(
      <RecurringForm
        item={r}
        names={s!.categories}
        onSave={async (tolerance, category, incomeStream, amount) => {
          if (await act(
            {
              type: "recurring",
              recurringId: r.id,
              tolerance,
              category,
              incomeStream,
              amount,
            },
            "Recurring item updated.",
          )) setModal(null);
        }}
        onDismiss={async () => {
          if (await act(
            { type: "dismiss-recurring", recurringId: r.id },
            "Pattern hidden. You can restore it from recurring.",
          )) setModal(null);
        }}
      />,
    );
  }
  const swipePending = s?.transactions.filter(t => t.status === "pending" && !t.bankPending) || [];
  const swipeCurrent = swipePending.find(t => t.id === reviewId) || swipePending[0];
  const swipeCategory = reviewCategory || swipeCurrent?.suggested || "spending";
  const swipeSubcategory = reviewCategory ? reviewSubcategoryId : reviewSubcategoryId || swipeCurrent?.suggestedSubcategoryId;
  const reviewSwipe = useSwipeCard(swipeCurrent?.id || "", () => swipeCurrent ? act({ type: "review", transactionId: swipeCurrent.id, category: swipeCategory, subcategoryId: swipeSubcategory, incomeStream: swipeCurrent.direction === "in" ? stream : undefined }, "Confirmed. One less thing on your mind.") : false, () => setReviewPicker(true), !reviewCategory && !reviewPicker, reviewPicker);
  const swipeRecurring = s?.recurring.find(r => !r.dismissed && (!r.confirmed || r.mismatch));
  const recurringSwipe = useSwipeCard(swipeRecurring?.id || "", () => { const form = document.querySelector<HTMLFormElement>(".recurring-review form"); if (!form?.checkValidity()) { form?.reportValidity(); return false; } form.requestSubmit(); return true; }, () => { if (swipeRecurring) recurringEdit(swipeRecurring); });
  const reviewSheetDrag = useSheetDrag(reviewPicker, () => setReviewPicker(false));
  const historySheetDrag = useSheetDrag(historyFiltersOpen, () => setHistoryFiltersOpen(false));
  useSheetFocus(reviewPicker || historyFiltersOpen);
  if (auth === "loading")
    return (
      <div className="loading">
        <Mark />
        <p>getting your bearings…</p>
      </div>
    );
  if (auth !== "ready" || !s)
    return (
      <div className="auth-shell">
        <div className="auth-brand">
          <Mark />
          <b>sofar</b>
        </div>
        <AuthForm
          setup={auth === "setup"}
          onComplete={async () => {
            setState(await flush());
            const info = await api("/auth/status");
            setTotpEnabled(!!info.totpEnabled);
            setAuth("ready");
          }}
          onDemo={async () => {
            setState(await loadDemo());
            setTotpEnabled(false);
            setAuth("ready");
          }}
        />
      </div>
    );
  const period = new Date();
  period.setMonth(period.getMonth() + monthOffset, 1);
  const b = budget(s, period);
  const pending = s.transactions.filter(
    (t) => t.status === "pending" && !t.bankPending,
  );
  const candidates = s.recurring.filter(
    (r) => !r.dismissed && (!r.confirmed || r.mismatch),
  );
  const count = pending.length + candidates.length;
  const current = pending.find((t) => t.id === reviewId) || pending[0];
  const reviewPosition = pending.findIndex((t) => t.id === current?.id);
  function skipReview() {
    if (pending.length < 2) return;
    setReviewId(pending[(reviewPosition + 1) % pending.length].id);
    setReviewCategory(null);
    setReviewSubcategoryId(undefined);
    setReviewPicker(false);
    setStream("salary");
  }
  const selected = reviewCategory || current?.suggested || "spending";
  const selectedSubcategoryId = reviewCategory ? reviewSubcategoryId : reviewSubcategoryId || current?.suggestedSubcategoryId;
  const mobileActive: Page = ["Calculators", "Should I buy this", "Categories", "Sorting rules", "Settings"].includes(page) ? "Calculators" : ["Accounts", "Savings goal", "Recurring"].includes(page) ? "Overview" : page;
  const recent = [...s.transactions]
    .filter(
      (t) =>
        t.status === "confirmed" &&
        t.date.slice(0, 7) ===
          `${period.getFullYear()}-${String(period.getMonth() + 1).padStart(2, "0")}`,
    )
    .sort((a, b) => b.date.localeCompare(a.date));
  const rows = s.transactions
    .filter(
      (t) =>
        (filter === "all" ||
          (filter === "pending" && t.status === "pending") ||
          t.category === filter) &&
        (accountFilter === "all" || t.accountId === accountFilter) &&
        (historyDate === "all time" || t.date.slice(0, 7) === `${new Date().getFullYear()}-${String(new Date().getMonth() + 1).padStart(2, "0")}`) &&
        (t.merchant.toLowerCase().includes(search.toLowerCase()) ||
          String(t.amount / 100).includes(search.replace("$", "")) ||
          (s.accounts.find(a => a.id === t.accountId)?.name || "").toLowerCase().includes(search.toLowerCase())),
    )
    .sort((a, b) => b.date.localeCompare(a.date));
  const net = (t: Transaction) =>
    t.amount -
    s.links
      .filter((l) => l.expenseId === t.id)
      .reduce((n, l) => n + l.amount, 0);
  function txRow(t: Transaction) {
    const link = s!.links.find(
      (l) => l.expenseId === t.id || l.creditId === t.id,
    );
    return (
      <div className="history-item" key={t.id}>
      <div
        className="transaction-row"
        role="button"
        tabIndex={0}
        aria-expanded={expandedTransaction === t.id}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            setExpandedTransaction(expandedTransaction === t.id ? null : t.id);
          }
        }}
        onClick={(e) => {
          if (!(e.target as HTMLElement).closest("button"))
            setExpandedTransaction(expandedTransaction === t.id ? null : t.id);
        }}
      >
        <Merchant name={t.merchant} />
        <div className="transaction-name">
          <strong>{t.merchant}</strong>
          <span>
            {s!.accounts.find((a) => a.id === t.accountId)?.name || "Account"}{" "}
            · {t.status === "pending" ? "needs review" : t.category ? s!.categories[t.category] : "income"}{" "}
            <span className="mobile-date">· {dateLabel(t.date)}</span>
          </span>
        </div>
        <span className="tx-date">{dateLabel(t.date)}</span>
        <span
          className={`pill ${t.status === "pending" ? "pending" : t.incomeStream ? "income" : t.category || ""}`}
        >
          {t.status === "pending"
            ? "Needs review"
            : t.incomeStream === "salary"
              ? "Salary"
              : t.incomeStream === "self-employed"
                ? "Self-employed"
                : t.incomeStream === "transfer"
                  ? "Transfer"
                  : link
                    ? "Reimbursed"
                    : t.category
                      ? s!.categories[t.category]
                      : "Transfer"}
        </span>
        <strong
          className={`tx-amount ${t.direction === "in" ? "positive" : ""}`}
        >
          {t.direction === "in" ? "+" : "−"}
          {money(net(t), true)}
        </strong>
        <button
          className="icon-button tx-more"
          aria-label={`Details for ${t.merchant}`}
          onClick={() =>
            setModal(
              <div>
                <Merchant name={t.merchant} />
                <h2>{t.merchant}</h2>
                <p>
                  {dateLabel(t.date)} ·{" "}
                  {s!.accounts.find((a) => a.id === t.accountId)?.name}
                </p>
                <div className="detail-amount">{money(t.amount, true)}</div>
                {link ? (
                  <>
                    <p>{money(link.amount, true)} linked as a repayment.</p>
                    <button
                      className="button full"
                      onClick={async () => {
                        if (await act(
                          { type: "unlink", linkId: link.id },
                          "Repayment unlinked.",
                        )) setModal(null);
                      }}
                    >
                      unlink repayment
                    </button>
                  </>
                ) : t.direction === "in" ? (
                  <button className="button full" onClick={() => reimburse(t)}>
                    link as a repayment
                  </button>
                ) : null}
                <p className="field-label">category</p>
                <div className="category-options">
                  {categories.map((c) => (
                    <button
                      key={c}
                      className={`category-option ${t.category === c ? "selected" : ""}`}
                      onClick={async () => {
                        if (await act(
                          {
                            type: "review",
                            transactionId: t.id,
                            category: c,
                            incomeStream: t.incomeStream,
                          },
                          "Category updated. We’ll remember this merchant.",
                        )) setModal(null);
                      }}
                    >
                      {s!.categories[c]}
                    </button>
                  ))}
                  {(s!.subcategories || []).map(c => <button key={c.id} className={`category-option ${t.subcategoryId === c.id ? "selected" : ""}`} onClick={async () => {
                    if (await act({ type: "review", transactionId: t.id, category: c.group, subcategoryId: c.id, incomeStream: t.incomeStream }, "Category updated. We’ll remember this merchant.")) setModal(null);
                  }}>{c.name}</button>)}
                </div>
              </div>,
            )
          }
        >
          <MoreHorizontal size={18} />
        </button>
      </div>
      {expandedTransaction === t.id && <div className="history-inline-detail">
        <p>{dateLabel(t.date).toLowerCase()} · {s!.accounts.find(a => a.id === t.accountId)?.name.toLowerCase() || "account"} · {t.status === "pending" ? "needs review" : "confirmed"}</p>
        <div>
          <button onClick={(e) => e.currentTarget.closest(".history-item")?.querySelector<HTMLButtonElement>(".tx-more")?.click()}>change category</button>
          {t.category && <button onClick={() => act({ type: "review", transactionId: t.id, category: t.category || undefined, incomeStream: t.incomeStream }, "Rule saved for this merchant.")}>always sort like this</button>}
        </div>
      </div>}
      </div>
    );
  }
  return (
    <div className={`app-shell page-${page.toLowerCase().replace(/[^a-z]+/g, "-").replace(/-$/, "")}`}>
      <aside
        className="sidebar"
        inert={!!modal || compact}
      >
        <a
          className="brand"
          href="#"
          onClick={(e) => {
            e.preventDefault();
            go("Overview");
          }}
        >
          <Mark />
          <span>
            sofar<span className="brand-dot">.</span>
          </span>
        </a>
        <div className="workspace-label">your little money corner</div>
        <nav>
          {(["Overview", "Review inbox", "Transactions", "Accounts", "Savings goal", "Calculators"] as Page[]).map((p) => {
            const Icon = icons[p];
            return (
              <button
                key={p}
                className={`nav-item ${page === p ? "active" : ""}`}
                onClick={() => go(p)}
              >
                <Icon size={19} />
                {{ Overview: "home", "Review inbox": "review", Transactions: "history", Accounts: "accounts", "Savings goal": "goals", Calculators: "tools" }[p as "Overview" | "Review inbox" | "Transactions" | "Accounts" | "Savings goal" | "Calculators"]}
                {p === "Review inbox" && count > 0 && (
                  <span className="nav-count">{count}</span>
                )}
              </button>
            );
          })}
          <div className="nav-divider" />
          {(["Recurring", "Should I buy this"] as Page[]).map((p) => {
            const Icon = icons[p];
            return (
              <button
                key={p}
                className={`nav-item ${page === p ? "active" : ""}`}
                onClick={() => go(p)}
              >
                <Icon size={19} />
                {p}
              </button>
            );
          })}
        </nav>
        <div className="sidebar-bottom">
          <div className="sidebar-note">
            <div className="tiny-sprout">
              <Sprout size={23} />
            </div>
            <strong>a little clarity goes a long way.</strong>
            <p>
              just your money.
              <br />
              one day at a time.
            </p>
          </div>
          <button
            className={`nav-item ${page === "Settings" ? "active" : ""}`}
            onClick={() => go("Settings")}
          >
            <Settings2 size={19} />
            settings
          </button>
          <div className="profile">
            <span className="avatar">{s.demo ? "D" : "Y"}</span>
            <div>
              <strong>{s.demo ? "Demo workspace" : "Your workspace"}</strong>
              <span>
                {s.demo ? "Make yourself at home" : "Personal account"}
              </span>
            </div>
            <button
              className="icon-button"
              aria-label="account options"
              onClick={() => go("Settings")}
            >
              <ChevronDown size={16} />
            </button>
          </div>
        </div>
      </aside>
      <div className="main-shell" inert={!!modal}>
        {offline && (
          <div className="offline-banner">
            <CloudOff size={16} />
            you’re offline. your saved data is here, and changes will sync when
            you reconnect.
          </div>
        )}
        <main>
          <div className="page-heading">
            <div>
              <div className="eyebrow">
                {page === "Overview"
                  ? "A LITTLE CLARITY FOR YOUR MONEY"
                  : page === "Should I buy this"
                    ? "a moment to think it through"
                  : page === "Calculators"
                    ? "a few ways to look ahead"
                  : page === "Review inbox"
                    ? "A FEW SMALL DECISIONS"
                    : "YOUR MONEY, AT YOUR PACE"}
              </div>
              <h1>
                {page === "Overview"
                  ? "your money"
                  : page === "Should I buy this"
                    ? "should i buy this?"
                  : page === "Calculators"
                    ? "tools"
                  : page === "Categories"
                    ? "categories"
                  : page === "Sorting rules"
                    ? "sorting rules"
                  : page === "Review inbox"
                    ? "review"
                    : page === "Savings goal"
                      ? "goals"
                      : page === "Accounts"
                        ? "accounts"
                        : page === "Recurring"
                          ? "recurring"
                          : page === "Transactions"
                            ? "history"
                          : page === "Settings"
                            ? "settings"
                            : "tools"}
              </h1>
              <p>
                {page === "Overview"
                  ? "Real money. A clear picture. Room to breathe."
                  : page === "Should I buy this"
                    ? "a clearer view of the tradeoffs, before you decide."
                  : page === "Calculators"
                    ? "play with the numbers. see what changes."
                  : page === "Review inbox"
                    ? "A quick check now makes the rest a little clearer."
                    : page === "Transactions"
                      ? "Your transactions, with a place for each one."
                      : page === "Recurring"
                        ? "Know what’s committed before you spend."
                        : page === "Accounts"
                          ? "A simple view of the accounts you’ve connected."
                          : page === "Savings goal"
                            ? "Something to work toward, one month at a time."
                            : "Simple preferences for your own little money corner."}
              </p>
            </div>
            {page === "Overview" ? (
              <div className="month-picker">
                <button
                  aria-label="previous month"
                  onClick={() => setMonthOffset(monthOffset - 1)}
                >
                  <ChevronLeft size={16} />
                </button>
                <span>
                  {period.toLocaleDateString("en-US", {
                    month: "long",
                    year: "numeric",
                  })}
                </span>
                <button
                  aria-label="next month"
                  disabled={monthOffset === 0}
                  onClick={() => setMonthOffset(monthOffset + 1)}
                >
                  <ChevronRight size={16} />
                </button>
              </div>
            ) : page === "Accounts" ? (
              <div className="heading-actions">
                <button className="button" disabled={busy || offline} onClick={sync}>
                  <RefreshCw size={17} className={busy ? "spin" : ""} />
                  {busy ? "syncing…" : "sync now"}
                </button>
                <button className="button primary" onClick={() => linkBank()}>
                  <Plus size={17} />
                  connect an account
                </button>
              </div>
            ) : null}
          </div>
          {page === "Overview" && (
            <Dashboard
              state={s}
              period={period}
              count={count}
              transactions={recent}
              renderTransaction={txRow}
              onPage={go}
              onCategory={(category) => {
                go("Transactions");
                setFilter(category);
              }}
              onLink={() => linkBank()}
              onSync={sync}
              syncing={busy}
            />
          )}
          {page === "Should I buy this" && <ShouldIBuy state={s} onExplore={() => go("Calculators")} onDone={() => go("Calculators")} onSleep={(item, price) => {
            let saved: { item: string; price: number; date: string }[] = [];
            try {
              const stored = JSON.parse(localStorage.getItem("sofar-sleep-list") || "[]");
              if (Array.isArray(stored)) saved = stored;
            } catch { /* a damaged local list should not block saving a new item */ }
            localStorage.setItem("sofar-sleep-list", JSON.stringify([...saved, { item, price, date: new Date().toISOString() }]));
            setNotice("saved for later. you can find it in tools.");
            go("Calculators");
          }} />}
          {page === "Calculators" && <Calculators state={s} onBuy={() => go("Should I buy this")} onSetup={go} />}
          {page === "Categories" && <Categories state={s} onBack={() => go("Calculators")} onAction={act} />}
          {page === "Sorting rules" && <SortingRules state={s} onBack={() => go("Calculators")} onAction={act} />}
          {page === "Transactions" && (
            <section className="transaction-page">
              <div className="table-toolbar">
                <div className="search-field">
                  <input
                    aria-label="search transactions"
                    placeholder="search trader joe's, gas, 54…"
                    value={search}
                    onChange={(e) => setSearch(e.target.value)}
                  />
                </div>
                <button
                  className="history-export"
                  onClick={() => {
                    const csv = [
                      "Date,Merchant,Amount,Category,Status",
                      ...rows.map((t) =>
                        [
                          t.date,
                          `"${t.merchant.replace(/"/g, '""').replace(/^[=+@-]/, "'")}"`,
                          (t.direction === "out" ? -t.amount : t.amount) / 100,
                          t.category ? s.categories[t.category] : "",
                          t.status,
                        ].join(","),
                      ),
                    ].join("\n");
                    const url = URL.createObjectURL(
                      new Blob([csv], { type: "text/csv" }),
                    );
                    const a = document.createElement("a");
                    a.href = url;
                    a.download = "sofar-transactions.csv";
                    a.click();
                    URL.revokeObjectURL(url);
                  }}
                >
                  <Download size={16} />
                  export
                </button>
              </div>
              <div className="history-chips" role="group" aria-label="transaction categories">
                {["all", "expenses", "spending", "savings", "pending"] .map((value) => <button key={value} data-h="tick" className={filter === value ? "active" : ""} onClick={() => setFilter(value)}>{value === "all" ? "all" : value === "pending" ? "needs review" : s.categories[value as Category]}</button>)}
              </div>
              <div className="history-filter-buttons">
                <button data-h="sheet" onClick={() => setHistoryFiltersOpen(true)}>{accountFilter === "all" ? "all accounts" : s.accounts.find(a => a.id === accountFilter)?.name.toLowerCase()} <ChevronDown size={14} /></button>
                <button data-h="sheet" onClick={() => setHistoryFiltersOpen(true)}>{historyDate} <ChevronDown size={14} /></button>
              </div>
              <p className="history-summary">{rows.length} transactions · {money(rows.filter(t => t.direction === "out").reduce((sum, t) => sum + net(t), 0))} out · {money(rows.filter(t => t.direction === "in").reduce((sum, t) => sum + t.amount, 0))} in</p>
              {Array.from(new Set(rows.map(t => t.date))).map(date => <div className="history-day" key={date}>
                <h2>{date === new Date().toLocaleDateString("en-CA") ? "today" : date === new Date(Date.now() - 86400000).toLocaleDateString("en-CA") ? "yesterday" : new Date(date + "T12:00:00").toLocaleDateString("en-US", { weekday: "short", month: "short", day: "numeric" }).toLowerCase()}</h2>
                <div className="history-day-card">{rows.filter(t => t.date === date).map(txRow)}</div>
              </div>)}
              {!rows.length && (
                <div className="empty">
                  <Search />
                  <h3>no transactions found.</h3>
                  <p>try a different search or filter.</p>
                </div>
              )}
              {historyFiltersOpen && <div className="history-filter-backdrop" data-closing={historySheetDrag.closing} onClick={historySheetDrag.close}><div className="history-filter-sheet" role="dialog" aria-modal="true" aria-label="filter history" data-dragging={historySheetDrag.dragging} style={historySheetDrag.style} {...historySheetDrag.handlers} onClick={event => event.stopPropagation()}>
                <div className="history-sheet-handle" />
                <h2>filter</h2>
                <h3>account</h3>
                <div className="history-sheet-chips"><button data-h="tick" className={accountFilter === "all" ? "active" : ""} onClick={() => setAccountFilter("all")}>all accounts</button>{s.accounts.map(a => <button key={a.id} data-h="tick" className={accountFilter === a.id ? "active" : ""} onClick={() => setAccountFilter(a.id)}>{a.name.toLowerCase()}</button>)}</div>
                <h3>when</h3>
                <div className="history-sheet-chips"><button data-h="tick" className={historyDate === "this month" ? "active" : ""} onClick={() => setHistoryDate("this month")}>this month</button><button data-h="tick" className={historyDate === "all time" ? "active" : ""} onClick={() => setHistoryDate("all time")}>all time</button></div>
                <div className="history-sheet-actions"><button data-h="soft" onClick={() => { setFilter("all"); setAccountFilter("all"); setHistoryDate("this month"); setSearch(""); }}>clear</button><button data-h="success" onClick={historySheetDrag.close}>show {rows.length}</button></div>
              </div></div>}
            </section>
          )}
          {page === "Review inbox" && (
            <div className="review-layout">
              <section className="review-main">
                {current ? (
                  <>
                    <div className="review-progress">
                      <span>{count} left</span>
                      <span>transaction {reviewPosition + 1} of {pending.length}</span>
                    </div>
                    <div className="review-progress-track"><span style={{ width: `${((reviewPosition + 1) / Math.max(1, pending.length)) * 100}%` }} /></div>
                    <div className="review-card-stage" style={reviewSwipe.style}>
                    <div className="review-drag-tint review-drag-confirm" style={{ opacity: Math.min(.45, Math.max(0, reviewSwipe.x / 70) * .45) }} />
                    <div className="review-drag-tint review-drag-change" style={{ opacity: Math.min(.5, Math.max(0, -reviewSwipe.x / 70) * .5) }} />
                    <span className="review-drag-cue review-drag-cue-confirm" aria-hidden="true" style={{ opacity: Math.max(0, Math.min(1, reviewSwipe.x / 70)), transform: `scale(${reviewSwipe.x > 90 ? 1.12 : .8})` }}>confirm ✓</span>
                    <span className="review-drag-cue review-drag-cue-change" aria-hidden="true" style={{ opacity: Math.max(0, Math.min(1, -reviewSwipe.x / 70)), transform: `scale(${reviewSwipe.x < -90 ? 1.12 : .8})` }}>change ←</span>
                    <div className="review-card card" data-phase={reviewSwipe.phase} {...reviewSwipe.handlers}>
                      <div className="review-kind">new transaction</div>
                      <Merchant name={current.merchant} />
                      <h2>{current.merchant}</h2>
                      <div className="review-amount">
                        {current.direction === "in" ? "+" : "−"}
                        {money(current.amount, true)}
                      </div>
                      <div className="review-card-meta">
                        <span>{s.accounts.find((a) => a.id === current.accountId)?.name?.toLowerCase() || "account"} · {s.accounts.find((a) => a.id === current.accountId)?.mask || ""}</span>
                        <span>· {dateLabel(current.date).toLowerCase()}</span>
                      </div>
                      <p>
                        {current.direction === "in"
                          ? "Money came in. What kind is it?"
                          : "Looks like this belongs in…"}
                      </p>
                      {current.direction === "in" ? (
                        <div className="field">
                          <label htmlFor="income-stream">income type</label>
                          <select
                            id="income-stream"
                            value={stream}
                            onChange={(e) => setStream(e.target.value)}
                          >
                            <option value="salary">regular salary</option>
                            <option value="self-employed">
                              self-employment income
                            </option>
                            <option value="transfer">
                              transfer between my accounts
                            </option>
                          </select>
                          <button
                            className="text-button"
                            onClick={() => reimburse(current)}
                          >
                            <Link2 size={16} />
                            this is a repayment
                          </button>
                          {reimbursementMatches(s, current)
                            .filter(
                              (t) =>
                                t.amount >= current.amount &&
                                t.amount <= current.amount * 2 &&
                                Date.parse(current.date) - Date.parse(t.date) <=
                                  60 * 86400000,
                            )
                            .slice(0, 1)
                            .map((t) => (
                              <button
                                key={t.id}
                                className="repayment-suggestion"
                                onClick={() => reimburse(current, t.id)}
                              >
                                <Link2 size={16} />
                                <span>
                                  possible repayment for{" "}
                                  <strong>{t.merchant}</strong>
                                  <small>
                                    {money(t.amount, true)} ·{" "}
                                    {dateLabel(t.date)}
                                  </small>
                                </span>
                                <ChevronRight size={16} />
                              </button>
                            ))}
                        </div>
                      ) : <div className="review-suggestion"><span>we think it’s</span><strong>{s.categories[selected]}</strong><small>{(s.subcategories || []).find(c => c.id === selectedSubcategoryId)?.name || "you can change this"}</small></div>}
                      {pending.length > 1 && (
                        <button
                          className="review-skip"
                          onClick={skipReview}
                          type="button"
                        >
                          skip for now
                          <ArrowRight size={16} />
                        </button>
                      )}
                      <div className="swipe-hint">
                        swipe left to change · swipe right to confirm
                      </div>
                    </div>
                    </div>
                    <div className="review-actions">
                      <button className="button" data-h="sheet" onClick={() => setReviewPicker(!reviewPicker)}><ArrowLeft size={17} />change category</button>
                      <button className="button primary" data-h="success" onClick={() => reviewSwipe.confirm(true)}><Check size={17} />confirm</button>
                    </div>
                    {reviewPicker && (
                      <div className="design-sheet-backdrop" data-closing={reviewSheetDrag.closing} onClick={reviewSheetDrag.close}><div className="design-sheet" role="dialog" aria-modal="true" aria-label="change category" data-dragging={reviewSheetDrag.dragging} style={reviewSheetDrag.style} {...reviewSheetDrag.handlers} onClick={event => event.stopPropagation()}>
                        <div className="history-sheet-handle" /><div className="design-sheet-title"><h2>change category</h2><button aria-label="close" onClick={reviewSheetDrag.close}><X size={20} /></button></div>
                        <div className="design-sheet-chips">{categories.map(c => <button key={c} data-h="tick" className={selected === c ? "active" : ""} onClick={() => { setReviewCategory(c); setReviewSubcategoryId(undefined); }}>{s.categories[c]}</button>)}</div>
                        {(s.subcategories || []).some(c => c.group === selected) && <><span className="design-sheet-label">more specific</span><div className="design-sheet-chips"><button data-h="tick" className={!selectedSubcategoryId ? "active" : ""} onClick={() => setReviewSubcategoryId(undefined)}>just {s.categories[selected]}</button>{(s.subcategories || []).filter(c => c.group === selected).map(c => <button key={c.id} data-h="tick" className={selectedSubcategoryId === c.id ? "active" : ""} onClick={() => setReviewSubcategoryId(c.id)}>{c.name}</button>)}</div></>}
                        <button className="design-sheet-save" data-h="success" onClick={reviewSheetDrag.close}>use this category</button>
                      </div></div>
                    )}
                  </>
                ) : candidates.length ? (
                  <div className="review-card-stage" style={recurringSwipe.style}>
                  <div className="review-drag-tint review-drag-confirm" style={{ opacity: Math.min(.45, Math.max(0, recurringSwipe.x / 70) * .45) }} />
                  <div className="review-drag-tint review-drag-change" style={{ opacity: Math.min(.5, Math.max(0, -recurringSwipe.x / 70) * .5) }} />
                  <div
                    key={candidates[0].id}
                    className="review-card card recurring-review"
                    data-phase={recurringSwipe.phase}
                    {...recurringSwipe.handlers}
                  >
                    <div className="review-progress">
                      <span>{candidates.length} recurring items left</span>
                      <span>
                        {candidates[0].mismatch
                          ? "An amount changed"
                          : "A pattern worth a look"}
                      </span>
                    </div>
                    <RecurringForm
                      item={candidates[0]}
                      names={s.categories}
                      onSave={(tolerance, category, incomeStream, amount) =>
                        act(
                          {
                            type: "recurring",
                            recurringId: candidates[0].id,
                            tolerance,
                            category,
                            incomeStream,
                            amount,
                          },
                          "Recurring item confirmed.",
                        )
                      }
                      onDismiss={() =>
                        act(
                          {
                            type: "dismiss-recurring",
                            recurringId: candidates[0].id,
                          },
                          "Pattern hidden. You can restore it from recurring.",
                        )
                      }
                    />
                    <div className="swipe-hint">
                      swipe left to edit · swipe right to confirm
                    </div>
                  </div>
                  </div>
                ) : (
                  <div className="card empty">
                    <span className="empty-icon">
                      <CheckCheck size={33} />
                    </span>
                    <h2>all caught up.</h2>
                    <p>your transactions have a home. that’s one less thing.</p>
                    <button className="button" onClick={() => go("Overview")}>
                      back to your overview
                      <ArrowRight size={16} />
                    </button>
                  </div>
                )}
                {current && candidates.length > 0 && (
                  <section className="card candidates">
                    <h2>a pattern worth a look</h2>
                    {candidates.map((r) => (
                      <div className="recurring-row" key={r.id}>
                        <Merchant name={r.merchant} />
                        <div>
                          <strong>{r.merchant}</strong>
                          <span>
                            {r.mismatch
                              ? "Amount changed"
                              : `Looks ${r.cadence}`}{" "}
                            · {money(r.amount, true)}
                          </span>
                        </div>
                        <button
                          className="button small-button"
                          onClick={() => recurringEdit(r)}
                        >
                          review
                        </button>
                      </div>
                    ))}
                  </section>
                )}
              </section>
              <aside className="review-aside">
                <span className="icon-disc">
                  <Leaf size={22} />
                </span>
                <h3>
                  a little attention.
                  <br /> a clearer picture.
                </h3>
                <p>
                  these transactions stay out of your budget until you confirm
                  them.
                </p>
                <div className="aside-line" />
                <h4>it gets easier.</h4>
                <p>
                  when you change a category, sofar remembers the merchant for
                  next time.
                </p>
                <h4>someone paid you back?</h4>
                <p>
                  link the repayment to the original purchase. only your share
                  counts, even across accounts.
                </p>
              </aside>
            </div>
          )}
          {page === "Recurring" && (
            <>
              <div className="summary-strip">
                <div>
                  <span>committed monthly bills</span>
                  <strong>{money(b.bills, true)}</strong>
                </div>
                <div>
                  <span>confirmed recurring items</span>
                  <strong>
                    {s.recurring.filter((r) => r.confirmed && !r.dismissed).length}
                  </strong>
                </div>
                <div>
                  <span>detection threshold</span>
                  <strong>
                    {s.threshold}
                    <small> occurrences</small>
                  </strong>
                </div>
              </div>
              <section className="card">
                {s.recurring.filter((r) => !r.dismissed).map((r) => (
                  <div className="recurring-row" key={r.id}>
                    <Merchant name={r.merchant} />
                    <div className="grow">
                      <strong>{r.merchant}</strong>
                      <span>
                        {r.cadence} ·{" "}
                        {r.type === "income"
                          ? r.incomeStream
                          : s.categories[r.category]}{" "}
                        · {r.tolerance}% amount tolerance
                      </span>
                    </div>
                    <span
                      className={`pill ${r.confirmed ? "expenses" : "pending"}`}
                    >
                      {r.mismatch
                        ? "Amount changed"
                        : r.confirmed
                          ? "Confirmed"
                          : "Needs review"}
                    </span>
                    <strong>{money(r.amount, true)}</strong>
                    <button
                      className="icon-button"
                      aria-label={`Edit ${r.merchant}`}
                      onClick={() => recurringEdit(r)}
                    >
                      <Settings2 size={18} />
                    </button>
                  </div>
                ))}
                {!s.recurring.some((r) => !r.dismissed) && (
                  <div className="empty">
                    <Repeat2 />
                    <h3>patterns will find their way here.</h3>
                    <p>
                      after {s.threshold} similar transactions, we’ll ask you to
                      confirm.
                    </p>
                  </div>
                )}
              </section>
              {s.recurring.some((r) => r.dismissed) && (
                <details className="card dismissed-patterns">
                  <summary>hidden patterns ({s.recurring.filter((r) => r.dismissed).length})</summary>
                  {s.recurring.filter((r) => r.dismissed).map((r) => (
                    <div className="recurring-row" key={r.id}>
                      <Merchant name={r.merchant} />
                      <div className="grow">
                        <strong>{r.merchant}</strong>
                        <span>{r.cadence} · {money(r.amount, true)}</span>
                      </div>
                      <button
                        className="button small-button"
                        onClick={() => act(
                          { type: "restore-recurring", recurringId: r.id },
                          "Pattern restored.",
                        )}
                      >
                        restore
                      </button>
                    </div>
                  ))}
                </details>
              )}
              <p className="footnote">
                monthly commitments include only confirmed recurring bills in{" "}
                {s.categories.expenses}. each bill has its own amount tolerance.
              </p>
            </>
          )}
          {page === "Savings goal" && (
            <div className="savings-layout">
              <section className="card savings-main">
                <div className="section-title">
                  <span className="pill savings">your active goal</span>
                  <button className="text-button" onClick={editGoal}>
                    edit goal
                    <Settings2 size={16} />
                  </button>
                </div>
                <div className="big-plant">
                  <Sprout strokeWidth={1} size={100} />
                </div>
                <h2>{s.goal.name}</h2>
                <p>a little set aside today. more room tomorrow.</p>
                <div className="savings-amount">
                  {money(s.goal.saved)}
                  <span> / {money(s.goal.target)}</span>
                </div>
                <div className="progress-track">
                  <i
                    style={{
                      width: `${Math.min(100, (s.goal.saved / Math.max(1, s.goal.target)) * 100)}%`,
                    }}
                  />
                </div>
                <div className="goal-values">
                  <span>
                    {Math.round(
                      (s.goal.saved / Math.max(1, s.goal.target)) * 100,
                    )}
                    % of the way there
                  </span>
                  <span>
                    {money(Math.max(0, s.goal.target - s.goal.saved))} to go
                  </span>
                </div>
                <div className="monthly-setting">
                  <div>
                    <span>monthly contribution</span>
                    <strong>{money(s.goal.monthly)}</strong>
                  </div>
                  <ShieldCheck size={24} />
                </div>
                <p className="footnote">
                  this contribution is already set aside in your safe-to-spend
                  amount. actual savings comes from confirmed transactions in
                  your savings category.
                </p>
              </section>
              <aside className="review-aside">
                <Sprout size={26} />
                <h3>slow is still forward.</h3>
                <p>you don’t need a perfect month to make a little progress.</p>
                <p>
                  your goal is a plan, not an automatic bank transfer. you stay
                  in control.
                </p>
              </aside>
            </div>
          )}
          {page === "Accounts" && (
            <>
              <div className="accounts-total">
                <span>total across connected accounts</span>
                <strong>
                  {money(
                    s.accounts.reduce((n, a) => n + a.balance, 0),
                    true,
                  )}
                </strong>
                <p>balances are separate from your spending allowance.</p>
              </div>
              <div className="account-grid">
                {s.accounts.map((a, i) => (
                  <section className="card account-card" key={a.id}>
                    <div className="section-title">
                      <span className={`bank-logo bank-${i}`}>
                        {a.institution.charAt(0)}
                      </span>
                      <span className="pill income">connected</span>
                    </div>
                    <p>{a.institution}</p>
                    <h2>{a.name}</h2>
                    <span className="muted">
                      •••• {a.mask} · {a.subtype || a.type}
                    </span>
                    <strong className="account-balance">
                      {money(a.balance, true)}
                    </strong>
                    <div className="account-foot">
                      <span className="status-dot" />
                      last synced{" "}
                      {a.syncedAt
                        ? new Date(a.syncedAt).toLocaleDateString()
                        : "—"}
                    </div>
                    <button
                      className="account-view"
                      onClick={() => {
                        go("Transactions");
                        setAccountFilter(a.id);
                      }}
                    >
                      view transactions
                      <ArrowRight size={16} />
                    </button>
                  </section>
                ))}
                <button
                  className="account-add"
                  onClick={() => linkBank("investments")}
                >
                  <span>
                    <Plus size={27} />
                  </span>
                  <h3>add an investment account</h3>
                  <p>a home for the bigger picture.</p>
                </button>
              </div>
              <div className="info-box">
                <ShieldCheck size={21} />
                <span>
                  your bank login stays with plaid. sofar only reads your
                  financial data.
                </span>
              </div>
            </>
          )}
          {page === "Settings" && (
            <div className="settings-layout">
              <section className="card settings-card">
                <h2>the basics</h2>
                <SettingsForm
                  state={s}
                  onSave={(threshold, names) =>
                    act(
                      { type: "settings", threshold, categories: names },
                      "Preferences saved.",
                    )
                  }
                />
              </section>
              <section className="card settings-card">
                <h2>your devices & security</h2>
                <div className="setting-line">
                  <div><strong>motion</strong><p>springy movement when you tap, swipe, and open a sheet. your device’s reduced-motion setting takes precedence.</p></div>
                  <select aria-label="motion style" value={motionMode} onChange={event => { const next = event.target.value as MotionMode; setMotionMode(next); localStorage.setItem("sofar-motion", next); }}><option value="expressive">expressive</option><option value="calm">calm</option><option value="off">off</option></select>
                </div>
                <div className="setting-line">
                  <div><strong>touch feedback</strong><p>distinct pulses for presses, choices, swipes, sheets, and confirmations on supported devices. visual ripples show feedback elsewhere.</p></div>
                  <span className="setting-actions"><button className="button small-button" type="button" data-h="success" disabled={!hapticsEnabled} aria-label="test touch feedback">feel it</button><button className="button small-button" role="switch" aria-checked={hapticsEnabled} data-h="manual" onClick={event => { const next = !hapticsEnabled; setHapticEnabled(true); emitHaptic(next ? "toggle" : "toggleOff", { x: event.clientX, y: event.clientY }); setHapticsEnabled(next); setHapticEnabled(next); localStorage.setItem("sofar-haptics", next ? "on" : "off"); }}>{hapticsEnabled ? "on" : "off"}</button></span>
                </div>
                <div className="setting-line">
                  <div>
                    <strong>review reminders</strong>
                    <p>
                      one push after a sync, with everything that needs a look.
                    </p>
                  </div>
                  <button
                    className="button small-button"
                    onClick={notifications}
                  >
                    <Bell size={16} />
                    {pushEnabled ? "turn off" : "enable"}
                  </button>
                </div>
                <div className="setting-line">
                  <div>
                    <strong>two-step verification</strong>
                    <p>
                      {totpEnabled
                        ? "on for your account. you’ll need a code when you sign in."
                        : "add an authenticator app to secure sign-in."}
                    </p>
                  </div>
                  {totpEnabled ? (
                    <span className="pill savings">on</span>
                  ) : <button
                    className="button small-button"
                    onClick={async () => {
                      if (s.demo) {
                        setNotice(
                          "Sign in to your server to set up two-step verification.",
                        );
                        return;
                      }
                      try {
                        const data = await api("/auth/totp/setup", {});
                        setModal(
                          <TOTPForm
                            secret={data.secret}
                            url={data.url}
                            onDone={() => {
                              setModal(null);
                              setTotpEnabled(true);
                              setNotice("Two-step verification is on.");
                            }}
                          />,
                        );
                      } catch (e) {
                        setNotice((e as Error).message);
                      }
                    }}
                  >
                    <ShieldCheck size={16} />
                    set up
                  </button>}
                </div>
                <div className="setting-line">
                  <div>
                    <strong>
                      {s.demo ? "Sample data" : "Signed in securely"}
                    </strong>
                    <p>
                      {s.demo
                        ? "Explore freely. Nothing here is connected to a bank."
                        : "Your session is stored in an HttpOnly cookie."}
                    </p>
                  </div>
                  <button
                    className="button small-button"
                    onClick={async () => {
                      if (s.demo) {
                        try {
                          const info = await api("/auth/status");
                          setAuth(info.setup ? "setup" : "login");
                        } catch {
                          setNotice(
                            "Start the Go server to use your own account.",
                          );
                        }
                      } else {
                        try {
                          await api("/auth/logout", {});
                          await clearPrivate();
                          setState(null);
                          setAuth("login");
                        } catch (e) {
                          setNotice((e as Error).message);
                        }
                      }
                    }}
                  >
                    <LogOut size={16} />
                    {s.demo ? "Sign in" : "Sign out"}
                  </button>
                </div>
                {s.demo && (
                  <div className="setting-line">
                    <div>
                      <strong>start the demo over</strong>
                      <p>restore the sample transactions and review queue.</p>
                    </div>
                    <button
                      className="button small-button"
                      onClick={() => setModal(
                        <div>
                          <h2>reset sample data?</h2>
                          <p>your changes in this demo will be cleared.</p>
                          <div className="review-actions">
                            <button className="button" onClick={() => setModal(null)}>keep them</button>
                            <button
                              className="button primary"
                              onClick={async () => {
                                const fresh = demoState();
                                await save(fresh);
                                setState(fresh);
                                setReviewId("");
                                setModal(null);
                                setNotice("Sample data restored.");
                              }}
                            >
                              reset demo
                            </button>
                          </div>
                        </div>,
                      )}
                    >
                      reset demo
                    </button>
                  </div>
                )}
              </section>
            </div>
          )}
          <footer className="page-footer">
            <span>
              <span className="status-dot" />
              {s.demo ? "Sample data · " : ""}last synced{" "}
              {new Date(s.lastSync).toLocaleTimeString("en-US", {
                hour: "numeric",
                minute: "2-digit",
              })}
              <button
                className="text-button"
                disabled={busy || offline}
                onClick={sync}
              >
                <RefreshCw size={13} className={busy ? "spin" : ""} />
                {busy ? "Syncing…" : "Sync now"}
              </button>
            </span>
            <span>
              so far, so good.
              <Leaf size={13} />
            </span>
          </footer>
        </main>
        <nav className="mobile-tabbar" aria-label="main navigation">
          {(
            [
              "Overview",
              "Review inbox",
              "Transactions",
              "Calculators",
            ] as Page[]
          ).map((p) => {
            const Icon = icons[p];
            return (
              <button
                key={p}
                className={mobileActive === p ? "active" : ""}
                onClick={() => go(p)}
                aria-current={mobileActive === p ? "page" : undefined}
              >
                <Icon size={18} />
                <span>{{ Overview: "home", "Review inbox": "review", Transactions: "history", Calculators: "tools" }[p as "Overview" | "Review inbox" | "Transactions" | "Calculators"]}</span>
                {p === "Review inbox" && count > 0 && (
                  <span className="tab-count">{count}</span>
                )}
              </button>
            );
          })}
        </nav>
      </div>
      {notice && (
        <div className="toast" role="status">
          {notice}
          <button
            aria-label="dismiss notification"
            onClick={() => setNotice("")}
          >
            <X size={16} />
          </button>
        </div>
      )}
      {modal && (
        <div className="modal-backdrop" onClick={() => setModal(null)}>
          <div
            className="modal"
            role="dialog"
            aria-modal="true"
            aria-label="details"
            onClick={(e) => e.stopPropagation()}
          >
            <button
              className="modal-close icon-button"
              aria-label="close dialog"
              onClick={() => setModal(null)}
            >
              <X size={20} />
            </button>
            {modal}
          </div>
        </div>
      )}
    </div>
  );
}
function GoalForm({
  goal,
  onSave,
}: {
  goal: State["goal"];
  onSave: (g: State["goal"]) => void;
}) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const d = new FormData(e.currentTarget);
        onSave({
          ...goal,
          name: String(d.get("name")),
          target: Math.round(Number(d.get("target")) * 100),
          monthly: Math.round(Number(d.get("monthly")) * 100),
        });
      }}
    >
      <span className="modal-symbol">
        <Sprout />
      </span>
      <h2>edit your goal</h2>
      <p>set a target and a comfortable monthly contribution.</p>
      <label className="field">
        goal name
        <input name="name" required maxLength={80} defaultValue={goal.name} />
      </label>
      <label className="field">
        target amount ($)
        <input
          name="target"
          type="number"
          min="1"
          max="100000000"
          step="0.01"
          required
          defaultValue={goal.target / 100}
        />
      </label>
      <label className="field">
        monthly contribution ($)
        <input
          name="monthly"
          type="number"
          min="0"
          max="100000000"
          step="0.01"
          required
          defaultValue={goal.monthly / 100}
        />
      </label>
      <button className="button primary full">
        save goal
        <Check size={17} />
      </button>
    </form>
  );
}
function SettingsForm({
  state,
  onSave,
}: {
  state: State;
  onSave: (n: number, c: State["categories"]) => void;
}) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const d = new FormData(e.currentTarget);
        onSave(
          Number(d.get("threshold")),
          Object.fromEntries(
            categories.map((c) => [c, String(d.get(c))]),
          ) as State["categories"],
        );
      }}
    >
      <label className="field">
        flag as recurring after
        <input
          type="number"
          name="threshold"
          min="2"
          max="24"
          required
          defaultValue={state.threshold}
        />
        <small>
          similar transactions, spaced weekly, biweekly, monthly, or annually.
        </small>
      </label>
      <h3>your three buckets</h3>
      <p className="subtle">
        same simple structure. names that make sense to you.
      </p>
      {categories.map((c) => (
        <label className="field" key={c}>
          {c.charAt(0).toUpperCase() + c.slice(1)}
          <input
            name={c}
            required
            maxLength={30}
            defaultValue={state.categories[c]}
          />
        </label>
      ))}
      <button className="button primary">save preferences</button>
    </form>
  );
}
function ReimbursementForm({
  state,
  credit,
  suggestedExpenseId,
  onSave,
}: {
  state: State;
  credit: Transaction;
  suggestedExpenseId?: string;
  onSave: (id: string, amount: number) => void;
}) {
  const suggestedExpense = state.transactions.find(
    (t) => t.id === suggestedExpenseId,
  );
  const suggestedRemaining = suggestedExpense
    ? suggestedExpense.amount - state.links
        .filter((l) => l.expenseId === suggestedExpense.id)
        .reduce((sum, l) => sum + l.amount, 0)
    : credit.amount;
  const [q, setQ] = useState(""),
    [id, setId] = useState(suggestedExpenseId || ""),
    [amount, setAmount] = useState(
      Math.min(credit.amount, suggestedRemaining) / 100,
    );
  const matches = reimbursementMatches(state, credit).filter((t) =>
    t.merchant.toLowerCase().includes(q.toLowerCase()),
  );
  const selectedExpense = state.transactions.find((t) => t.id === id);
  const available = selectedExpense
    ? selectedExpense.amount - state.links
        .filter((l) => l.expenseId === id)
        .reduce((sum, l) => sum + l.amount, 0)
    : credit.amount;
  const maximum = Math.min(credit.amount, available);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        onSave(id, Math.round(amount * 100));
      }}
    >
      <span className="modal-symbol">
        <Link2 />
      </span>
      <h2>link a repayment</h2>
      <p>
        match {money(credit.amount, true)} from {credit.merchant} to a purchase.
        we look across all your accounts.
      </p>
      <label className="field">
        find the original expense
        <input
          placeholder="search all accounts…"
          value={q}
          onChange={(e) => {
            setQ(e.target.value);
            setId("");
            setAmount(credit.amount / 100);
          }}
        />
      </label>
      <div className="match-list">
        {matches.map((t) => (
          <label
            className={`match ${id === t.id ? "selected" : ""}`}
            key={t.id}
          >
            <input
              type="radio"
              name="match"
              value={t.id}
              required
              checked={id === t.id}
              onChange={() => {
                setId(t.id);
                setAmount(
                  Math.min(
                    credit.amount,
                    t.amount -
                      state.links
                        .filter((l) => l.expenseId === t.id)
                        .reduce((n, l) => n + l.amount, 0),
                  ) / 100,
                );
              }}
            />
            <span>
              <strong>{t.merchant}</strong>
              <small>
                {dateLabel(t.date)} ·{" "}
                {state.accounts.find((a) => a.id === t.accountId)?.name}
              </small>
            </span>
            <b>{money(t.amount, true)}</b>
          </label>
        ))}
        {!matches.length && <p>no available expenses found.</p>}
      </div>
      <label className="field">
        amount to net ($)
        <input
          type="number"
          min="0.01"
          max={maximum / 100}
          step="0.01"
          required
          value={amount}
          onChange={(e) => setAmount(Number(e.target.value))}
        />
        {selectedExpense && <small>up to {money(maximum, true)} can be linked to this purchase.</small>}
      </label>
      <button className="button primary full" disabled={!id}>
        link repayment
        <Check size={17} />
      </button>
    </form>
  );
}
function RecurringForm({
  item,
  names,
  onSave,
  onDismiss,
}: {
  item: Recurring;
  names: State["categories"];
  onSave: (t: number, c: Category, stream?: string, amount?: number) => void;
  onDismiss?: () => void;
}) {
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const d = new FormData(e.currentTarget);
        onSave(
          Number(d.get("tolerance")),
          String(d.get("category")) as Category,
          item.type === "income" ? String(d.get("stream")) : undefined,
          Math.round(Number(d.get("amount")) * 100),
        );
      }}
    >
      <span className="modal-symbol">
        <Repeat2 />
      </span>
      <h2>{item.merchant}</h2>
      <p>
        {money(item.amount, true)} · {item.cadence}
      </p>
      <label className="field">
        expected amount ($)
        <input
          name="amount"
          type="number"
          min="0.01"
          max="100000000"
          step="0.01"
          required
          defaultValue={item.amount / 100}
        />
      </label>
      <label className="field">
        amount tolerance (%)
        <input
          name="tolerance"
          type="number"
          min="0"
          max="100"
          step="0.1"
          required
          defaultValue={item.tolerance}
        />
        <small>we’ll flag changes outside this range for review.</small>
      </label>
      <label className="field">
        category
        <select name="category" defaultValue={item.category}>
          {categories.map((c) => (
            <option key={c} value={c}>
              {names[c]}
            </option>
          ))}
        </select>
      </label>
      {item.type === "income" && (
        <label className="field">
          income stream
          <select name="stream" defaultValue={item.incomeStream || "salary"}>
            <option value="salary">salary</option>
            <option value="self-employed">self-employed</option>
          </select>
        </label>
      )}
      <button className="button primary full">
        {item.confirmed ? "Save changes" : "Confirm recurring item"}
      </button>
      {onDismiss && (
        <button className="button full" type="button" onClick={onDismiss}>
          {item.confirmed ? "stop tracking this pattern" : "not recurring"}
        </button>
      )}
    </form>
  );
}
function AuthForm({
  setup,
  onComplete,
  onDemo,
}: {
  setup: boolean;
  onComplete: () => Promise<void>;
  onDemo: () => void;
}) {
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  return (
    <form
      className="card auth-card"
      onSubmit={async (e: FormEvent<HTMLFormElement>) => {
        e.preventDefault();
        setBusy(true);
        const d = Object.fromEntries(new FormData(e.currentTarget));
        try {
          await api(setup ? "/auth/setup" : "/auth/login", d);
          await onComplete();
        } catch (e) {
          setError((e as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <span className="eyebrow">a little clarity for your money</span>
      <h1>{setup ? "set up sofar" : "welcome back"}</h1>
      <p>
        {setup
          ? "Create the one account for your own sofar."
          : "pick up where you left off."}
      </p>
      {setup && (
        <label className="field">
          setup key
          <input name="setupKey" type="password" required autoComplete="off" />
          <small>the sofar_setup_key from your server configuration.</small>
        </label>
      )}
      <label className="field">
        username
        <input name="username" required autoComplete="username" />
      </label>
      <label className="field">
        password
        <input
          name="password"
          type="password"
          required
          minLength={setup ? 12 : 1}
          autoComplete={setup ? "new-password" : "current-password"}
        />
      </label>
      {!setup && (
        <label className="field">
          authenticator code (if enabled)
          <input
            name="code"
            inputMode="numeric"
            autoComplete="one-time-code"
            pattern="[0-9]{6}"
            maxLength={6}
          />
        </label>
      )}
      {error && (
        <p className="error" role="alert">
          {error}
        </p>
      )}
      <button className="button primary full" disabled={busy}>
        {busy ? (
          <Loader2 className="spin" size={18} />
        ) : setup ? (
          "Create your account"
        ) : (
          "sign in"
        )}
        <ArrowRight size={17} />
      </button>
      <button className="text-button demo-link" type="button" onClick={onDemo}>
        take a look around with demo data
      </button>
    </form>
  );
}
function TOTPForm({
  secret,
  url,
  onDone,
}: {
  secret: string;
  url: string;
  onDone: () => void;
}) {
  const [error, setError] = useState("");
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        try {
          await api("/auth/totp/confirm", {
            code: new FormData(e.currentTarget).get("code"),
          });
          onDone();
        } catch (e) {
          setError((e as Error).message);
        }
      }}
    >
      <ShieldCheck />
      <h2>two-step verification</h2>
      <p>
        add this secret to your authenticator app as a time-based code, then
        enter the six-digit code below.
      </p>
      <code className="totp-secret">{secret}</code>
      <a className="text-button" href={url}>
        open authenticator app
        <ArrowUpRight size={16} />
      </a>
      <label className="field">
        six-digit code
        <input
          name="code"
          required
          pattern="[0-9]{6}"
          inputMode="numeric"
          autoComplete="one-time-code"
        />
      </label>
      {error && <p className="error">{error}</p>}
      <button className="button primary full">
        enable two-step verification
      </button>
    </form>
  );
}
