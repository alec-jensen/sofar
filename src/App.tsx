import Dashboard, { monthKey } from "./Dashboard";
import Onboarding from "./Onboarding";
import ShouldIBuy from "./ShouldIBuy";
import Calculators from "./Calculators";
import Categories from "./Categories";
import SortingRules from "./SortingRules";
import RecurringPage from "./Recurring";
import { configureMotion, emitHaptic, installFeedback, playPageEntrance, setHapticEnabled, type MotionMode } from "./motion";
import { useSwipeCard } from "./useSwipeCard";
import QRCode from "qrcode";
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
  nextDue,
  normalize,
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
    [motionMode, setMotionMode] = useState<MotionMode>(() => (localStorage.getItem("sofar-motion") === "off" ? "off" : "expressive"));
  const [search, setSearch] = useState(""),
    [filter, setFilter] = useState("all"),
    [accountFilter, setAccountFilter] = useState("all"),
    [historyDate, setHistoryDate] = useState(() => monthKey(new Date())),
    [historyFiltersOpen, setHistoryFiltersOpen] = useState(false),
    [expandedTransaction, setExpandedTransaction] = useState<string | null>(null),
    [reviewCategory, setReviewCategory] = useState<Category | null>(null),
    [reviewSubcategoryId, setReviewSubcategoryId] = useState<string | undefined>(undefined),
    [stream, setStream] = useState("salary"),
    [reviewPicker, setReviewPicker] = useState(false),
    [reviewAll, setReviewAll] = useState(true),
    [reviewId, setReviewId] = useState(""),
    [ignoreSheet, setIgnoreSheet] = useState(false),
    [ignoreReason, setIgnoreReason] = useState(""),
    [ignoreAlways, setIgnoreAlways] = useState(false),
    [splitSheet, setSplitSheet] = useState(false),
    [splitAmounts, setSplitAmounts] = useState<Record<Category, number>>({ expenses: 0, spending: 0, savings: 0 }),
    [depositSheet, setDepositSheet] = useState(false),
    [depositAmount, setDepositAmount] = useState(2500),
    [depositAccountId, setDepositAccountId] = useState(""),
    [depositLabel, setDepositLabel] = useState(""),
    [expandedAccount, setExpandedAccount] = useState<string | null>(null),
    [activeTxId, setActiveTxId] = useState(""),
    [onboarding, setOnboarding] = useState(false),
    [buyPrefill, setBuyPrefill] = useState<{ item: string; price: number } | undefined>(undefined);
  useEffect(() => {
    // The flag is per browser, so a new device with connected accounts skips setup.
    if (auth === "ready" && liveState.current && !localStorage.getItem("sofar-onboarded") && (liveState.current.demo || !liveState.current.accounts.length)) setOnboarding(true);
  }, [auth]);
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
    // Read the latest workspace, not this render's copy, so several actions
    // awaited in a row each build on the one before.
    const s = liveState.current;
    if (!s || actionInFlight.current) return false;
    actionInFlight.current = true;
    try {
      const next = await dispatch(s, a);
      liveState.current = next;
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
      setStream("");
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
    if (p !== "Should I buy this") setBuyPrefill(undefined);
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
        if (!s.connection?.connected) {
          openConnect();
          return;
        }
        const result = await api("/sync", {});
        const next = await flush();
        liveState.current = next;
        setState(next);
        setNotice(
          !result.fetched
            ? "Already up to date. SimpleFIN refreshes your banks about once a day."
            : next.connection?.error
              ? `Synced, with a problem: ${next.connection.error}`
              : "Synced with SimpleFIN.",
        );
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
  function openConnect() {
    const current = liveState.current;
    if (!current) return;
    setModal(
      <SimpleFinForm
        demo={current.demo}
        connected={!!current.connection?.connected}
        bridgeUrl={current.connection?.bridgeUrl}
        onConnected={async (warning) => {
          const next = await flush();
          liveState.current = next;
          setState(next);
          setModal(null);
          setNotice(warning ? `Connected, but the first sync needs another try: ${warning}` : `Connected. ${next.accounts.length} account${next.accounts.length === 1 ? "" : "s"} in.`);
        }}
      />,
    );
  }
  function confirmFirst(title: string, body: string, label: string, run: () => Promise<boolean>) {
    setModal(
      <div>
        <h2>{title}</h2>
        <p>{body}</p>
        <div className="review-actions">
          <button className="button" onClick={() => setModal(null)}>keep it</button>
          <button className="button danger" onClick={async () => { if (await run()) setModal(null); }}>{label}</button>
        </div>
      </div>,
    );
  }
  function removeAccount(account: State["accounts"][number]) {
    confirmFirst(
      `remove ${account.name.toLowerCase()}?`,
      "sofar hides this account and deletes its imported transactions, notes, and splits. to stop sharing it entirely, remove it in simplefin bridge too.",
      "remove",
      () => act({ type: "account-remove", accountId: account.id }, `${account.name} removed.`),
    );
  }
  function disconnectSimplefin() {
    confirmFirst(
      "disconnect simplefin?",
      "sofar forgets its simplefin access and deletes every imported account and transaction. rules, categories, bills, and entries you added by hand stay. revoke sofar in simplefin bridge too.",
      "disconnect",
      async () => {
        try {
          await api("/simplefin/disconnect", {});
          const next = await flush();
          liveState.current = next;
          setState(next);
          setNotice("SimpleFIN disconnected.");
          return true;
        } catch (e) {
          setNotice((e as Error).message);
          return false;
        }
      },
    );
  }
  function addBill() {
    setModal(
      <BillForm
        names={liveState.current!.categories}
        onSave={async (bill) => {
          if (await act({ type: "recurring-create", ...bill }, "Bill added. It’s set aside before safe to spend.")) setModal(null);
        }}
      />,
    );
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
  const sameMerchant = (t?: Transaction) => t ? swipePending.filter(o => o.id !== t.id && o.direction === t.direction && normalize(o.merchant) === normalize(t.merchant) && !s!.links.some(l => l.creditId === o.id)).length : 0;
  const swipeCurrent = swipePending.find(t => t.id === reviewId) || swipePending[0];
  const swipeCategory = reviewCategory || swipeCurrent?.suggested || "spending";
  // An explicit choice in the change-category sheet ("" means just the group) always beats the suggestion.
  const reviewChosen = reviewCategory !== null || reviewSubcategoryId !== undefined;
  const swipeSubcategory = reviewSubcategoryId !== undefined ? reviewSubcategoryId || undefined : reviewCategory ? undefined : swipeCurrent?.suggestedSubcategoryId;
  const confirmReview = (t: Transaction) => {
    if (t.direction === "out" && t.suggestedIgnore && !reviewChosen)
      return act({ type: "ignore", transactionId: t.id, reason: t.suggestedIgnore }, "Left out of your budget. It’s money moving, not spending.");
    if (t.direction === "in" && !stream) {
      setNotice("Choose what kind of money this is first.");
      return false;
    }
    const all = reviewAll && sameMerchant(t) > 0;
    return act(
      { type: "review", transactionId: t.id, category: swipeCategory, subcategoryId: swipeSubcategory, incomeStream: t.direction === "in" ? stream : undefined, always: all },
      all ? `Sorted all ${sameMerchant(t) + 1} from ${t.merchant}.` : "Confirmed. One less thing on your mind.",
    );
  };
  const reviewSwipe = useSwipeCard(swipeCurrent?.id || "", () => swipeCurrent ? confirmReview(swipeCurrent) : false, () => setReviewPicker(true), !reviewCategory && !reviewPicker, reviewPicker);
  useEffect(() => {
    // Start each incoming card on sofar's best guess.
    setStream(swipeCurrent?.suggestedIncomeStream || "");
  }, [swipeCurrent?.id, swipeCurrent?.suggestedIncomeStream]);
  const swipeRecurring = s?.recurring.find(r => !r.dismissed && (!r.confirmed || r.mismatch));
  const recurringSwipe = useSwipeCard(swipeRecurring?.id || "", () => { const form = document.querySelector<HTMLFormElement>(".recurring-review form"); if (!form?.checkValidity()) { form?.reportValidity(); return false; } form.requestSubmit(); return true; }, () => { if (swipeRecurring) recurringEdit(swipeRecurring); });
  const reviewSheetDrag = useSheetDrag(reviewPicker, () => setReviewPicker(false));
  const historySheetDrag = useSheetDrag(historyFiltersOpen, () => setHistoryFiltersOpen(false));
  const ignoreSheetDrag = useSheetDrag(ignoreSheet, () => setIgnoreSheet(false));
  const splitSheetDrag = useSheetDrag(splitSheet, () => setSplitSheet(false));
  const depositSheetDrag = useSheetDrag(depositSheet, () => setDepositSheet(false));
  useSheetFocus(reviewPicker || historyFiltersOpen || ignoreSheet || splitSheet || depositSheet);
  useEffect(() => {
    if (page !== "Review inbox" || modal || reviewPicker || historyFiltersOpen || ignoreSheet || splitSheet || depositSheet)
      return;
    const onKey = (e: KeyboardEvent) => {
      if (e.repeat || (e.key !== "ArrowLeft" && e.key !== "ArrowRight")) return;
      if ((e.target as HTMLElement).closest("input,textarea,select,[contenteditable]")) return;
      e.preventDefault();
      if (swipeCurrent) {
        if (e.key === "ArrowRight") reviewSwipe.confirm(true);
        else setReviewPicker(true);
      } else if (swipeRecurring) {
        if (e.key === "ArrowRight") {
          const form = document.querySelector<HTMLFormElement>(".recurring-review form");
          if (!form?.checkValidity()) form?.reportValidity();
          else form.requestSubmit();
        } else recurringEdit(swipeRecurring);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [page, modal, reviewPicker, historyFiltersOpen, ignoreSheet, splitSheet, depositSheet, swipeCurrent, swipeRecurring, reviewSwipe, recurringEdit]);
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
  const overlays = (
    <>
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
    </>
  );
  if (onboarding)
    return (
      <>
      <Onboarding
        state={s}
        onAction={act}
        onLinkBank={openConnect}
        onDone={() => {
          localStorage.setItem("sofar-onboarded", "1");
          setOnboarding(false);
        }}
      />
      {overlays}
      </>
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
    setStream("");
  }
  const selected = reviewCategory || current?.suggested || "spending";
  const selectedSubcategoryId = reviewSubcategoryId !== undefined ? reviewSubcategoryId || undefined : reviewCategory ? undefined : current?.suggestedSubcategoryId;
  const mobileActive: Page = ["Calculators", "Should I buy this", "Categories", "Sorting rules", "Settings"].includes(page) ? "Calculators" : ["Accounts", "Savings goal", "Recurring"].includes(page) ? "Overview" : page;
  const recent = [...s.transactions]
    .filter(
      (t) =>
        t.status === "confirmed" &&
        t.date.slice(0, 7) ===
          `${period.getFullYear()}-${String(period.getMonth() + 1).padStart(2, "0")}`,
    )
    .sort((a, b) => b.date.localeCompare(a.date));
  const activeTx = s.transactions.find((t) => t.id === activeTxId) || null;
  const savingsAmount = (t: Transaction) =>
    t.splits
      ? t.splits.filter((sp) => sp.category === "savings").reduce((n, sp) => n + sp.amount, 0)
      : t.category === "savings"
        ? t.amount - s.links.filter((l) => l.expenseId === t.id).reduce((n, l) => n + l.amount, 0)
        : 0;
  const savingsDeposits = s.transactions
    .filter((t) => t.status === "confirmed" && t.direction === "out" && !t.bankPending && !t.ignored && savingsAmount(t) > 0)
    .sort((a, b) => b.date.localeCompare(a.date));
  const savingsByMonth = Array.from({ length: 6 }, (_, i) => {
    const d = new Date();
    d.setMonth(d.getMonth() - (5 - i), 1);
    const key = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}`;
    return {
      key,
      label: d.toLocaleDateString("en-US", { month: "short" }),
      total: savingsDeposits
        .filter((t) => t.date.slice(0, 7) === key)
        .reduce((n, t) => n + savingsAmount(t), 0),
    };
  });
  const savingsMonthMax = Math.max(1, ...savingsByMonth.map((m) => m.total));
  const rows = s.transactions
    .filter(
      (t) =>
        (filter === "all" ||
          (filter === "pending" && t.status === "pending" && !t.bankPending) ||
          (filter === "income" && t.direction === "in" && t.status === "confirmed" && (t.incomeStream === "salary" || t.incomeStream === "self-employed" || t.incomeStream === "other")) ||
          (!t.ignored && (t.direction === "out" || s.links.some((l) => l.creditId === t.id)) && (t.category === filter || !!t.splits?.some((sp) => sp.category === filter)))) &&
        (accountFilter === "all" || t.accountId === accountFilter) &&
        (historyDate === "all time" || t.date.startsWith(historyDate)) &&
        (t.merchant.toLowerCase().includes(search.toLowerCase()) ||
          String(t.amount / 100).includes(search.replace("$", "")) ||
          (s.accounts.find(a => a.id === t.accountId)?.name || "").toLowerCase().includes(search.toLowerCase())),
    )
    .sort((a, b) => b.date.localeCompare(a.date));
  const historyMonths = Array.from(new Set([monthKey(new Date()), ...s.transactions.map((t) => t.date.slice(0, 7))]))
    .sort()
    .reverse()
    .slice(0, 12);
  const monthName = (key: string) => {
    if (key === "all time") return "all time";
    if (key === monthKey(new Date())) return "this month";
    const d = new Date(key + "-01T12:00:00");
    return d.toLocaleDateString("en-US", { month: "long", year: d.getFullYear() === new Date().getFullYear() ? undefined : "numeric" }).toLowerCase();
  };
  const net = (t: Transaction) =>
    t.amount -
    s.links
      .filter((l) => l.expenseId === t.id)
      .reduce((n, l) => n + l.amount, 0);
  const subName = (id?: string) => (id ? s!.subcategories?.find((c) => c.id === id)?.name : undefined);
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
            · {t.bankPending ? "processing at the bank" : t.status === "pending" ? "needs review" : t.ignored ? "ignored" : t.splits ? "split" : t.direction === "in" && !link ? "money in" : t.category ? s!.categories[t.category] : "income"}{" "}
            <span className="mobile-date">· {dateLabel(t.date)}</span>
          </span>
        </div>
        <span className="tx-date">{dateLabel(t.date)}</span>
        <span
          className={`pill ${t.status === "pending" ? "pending" : t.ignored ? "pending" : t.splits ? "spending" : t.incomeStream ? "income" : t.category || ""}`}
        >
          {t.bankPending
            ? "Processing"
            : t.status === "pending"
            ? "Needs review"
            : t.ignored
              ? "Ignored"
              : t.splits
                ? "Split"
                : t.incomeStream === "salary"
                  ? "Salary"
                  : t.incomeStream === "self-employed"
                    ? "Self-employed"
                    : t.incomeStream === "other"
                      ? "Other income"
                      : t.incomeStream === "transfer"
                      ? "Transfer"
                      : link
                        ? "Reimbursed"
                        : t.category
                          ? subName(t.subcategoryId) || s!.categories[t.category]
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
          onClick={() => {
            setModal(
              <div>
                <Merchant name={t.merchant} />
                <h2>{t.merchant}</h2>
                <p>
                  {dateLabel(t.date)} ·{" "}
                  {s!.accounts.find((a) => a.id === t.accountId)?.name}
                </p>
                <div className="detail-amount">{money(t.amount, true)}</div>
                {t.description && t.description !== t.merchant && (
                  <p className="detail-description">bank says: {t.description}</p>
                )}
                {t.ignored && (
                  <p className="detail-ignored">
                    ignored{t.ignoreReason ? ` · ${t.ignoreReason}` : ""}. it doesn’t count toward your budget.
                  </p>
                )}
                {t.splits && (
                  <div className="split-rows">
                    {t.splits.map((sp, i) => (
                      <div className="split-row" key={i}>
                        <span>{s!.categories[sp.category]}</span>
                        <strong>{money(sp.amount, true)}</strong>
                      </div>
                    ))}
                  </div>
                )}
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
                {t.bankPending && (
                  <p className="detail-ignored">still processing at the bank. you can sort it once it posts.</p>
                )}
                {!t.bankPending && t.direction === "in" && !link && (
                  <>
                    <p className="field-label">what is this money?</p>
                    <div className="category-options">
                      {([["salary", "paycheck"], ["self-employed", "self-employed"], ["other", "other income"], ["transfer", "transfer between my accounts"]] as const).map(([value, label]) => (
                        <button
                          key={value}
                          className={`category-option ${t.status === "confirmed" && t.incomeStream === value ? "selected" : ""}`}
                          onClick={async () => {
                            if (await act({ type: "review", transactionId: t.id, category: t.category || t.suggested || "spending", incomeStream: value }, "Income updated.")) setModal(null);
                          }}
                        >
                          {label}
                        </button>
                      ))}
                    </div>
                  </>
                )}
                {!t.splits && !t.bankPending && t.direction === "out" && (
                  <>
                    <p className="field-label">category</p>
                    <CategoryPicker
                      state={s!}
                      current={t}
                      onPick={async (category, subcategoryId) => {
                        if (await act({ type: "review", transactionId: t.id, category, subcategoryId, incomeStream: t.incomeStream }, "Category updated. We’ll remember this merchant.")) setModal(null);
                      }}
                    />
                  </>
                )}
                <p className="field-label">note</p>
                <div className="detail-note">
                  <input
                    placeholder="add a note…"
                    defaultValue={t.note || ""}
                    onBlur={(e) => {
                      if (e.currentTarget.value !== (t.note || "")) act({ type: "note", transactionId: t.id, note: e.currentTarget.value }, "Note saved.");
                    }}
                  />
                </div>
                {!t.bankPending && <div className="design-sheet-extras">
                  {t.direction === "out" && !link && (
                    <button data-h="tap" onClick={() => { setActiveTxId(t.id); setSplitAmounts({ expenses: 0, spending: 0, savings: 0, [t.category || "spending"]: t.amount / 100 } as Record<Category, number>); setSplitSheet(true); }}>split it</button>
                  )}
                  {!t.ignored && !t.manual && <button data-h="tap" onClick={() => { setActiveTxId(t.id); setIgnoreReason(""); setIgnoreAlways(false); setIgnoreSheet(true); }}>ignore</button>}
                  {t.manual && <button data-h="thud" onClick={() => confirmFirst(`delete “${t.merchant.toLowerCase()}”?`, "you added this entry yourself. deleting it removes it from your history and your savings progress.", "delete", () => act({ type: "delete-transaction", transactionId: t.id }, "Entry deleted."))}>delete this entry</button>}
                </div>}
              </div>,
            );
          }}
        >
          <MoreHorizontal size={18} />
        </button>
      </div>
      {expandedTransaction === t.id && <div className="history-inline-detail">
        <p>{dateLabel(t.date).toLowerCase()} · {s!.accounts.find(a => a.id === t.accountId)?.name.toLowerCase() || "account"} · {t.bankPending ? "processing at the bank" : t.status === "pending" ? "needs review" : "confirmed"}{t.note ? ` · ${t.note}` : ""}</p>
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
                {p === "Recurring" ? "subscriptions" : p}
              </button>
            );
          })}
        </nav>
        <div className="sidebar-bottom">
          <button
            className={`nav-item ${page === "Settings" ? "active" : ""}`}
            onClick={() => go("Settings")}
          >
            <Settings2 size={19} />
            settings
          </button>
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
                          ? "subscriptions & recurring"
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
                        ? "What repeats, what it costs, and what’s coming up."
                        : page === "Accounts"
                          ? "A simple view of the accounts you’ve connected."
                          : page === "Savings goal"
                            ? "Something to work toward, one month at a time."
                            : "Simple preferences for your own little money corner."}
              </p>
            </div>
            {page === "Recurring" ? (
              <div className="heading-actions">
                <button className="button primary" onClick={addBill}>
                  <Plus size={17} />
                  add a bill
                </button>
              </div>
            ) : page === "Accounts" ? (
              <div className="heading-actions">
                <button className="button" disabled={busy || offline} onClick={sync}>
                  <RefreshCw size={17} className={busy ? "spin" : ""} />
                  {busy ? "syncing…" : "sync now"}
                </button>
                <button className="button primary" onClick={openConnect}>
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
              onCategory={(category, month) => {
                go("Transactions");
                setFilter(category);
                setHistoryDate(month);
              }}
              onMonth={(delta) => setMonthOffset(Math.min(0, monthOffset + delta))}
              canGoForward={monthOffset < 0}
              onLink={openConnect}
              onSync={sync}
              syncing={busy}
            />
          )}
          {page === "Should I buy this" && <ShouldIBuy key={buyPrefill ? `${buyPrefill.item}-${buyPrefill.price}` : "blank"} initial={buyPrefill} state={s} onExplore={() => go("Calculators")} onDone={() => go("Calculators")} onSleep={(item, price) => {
            let saved: { item: string; price: number; date: string }[] = [];
            try {
              const stored = JSON.parse(localStorage.getItem("sofar-sleep-list") || "[]");
              if (Array.isArray(stored)) saved = stored;
            } catch { /* a damaged local list should not block saving a new item */ }
            localStorage.setItem("sofar-sleep-list", JSON.stringify([...saved, { item, price, date: new Date().toISOString() }]));
            setNotice("saved for later. you can find it in tools.");
            go("Calculators");
          }} />}
          {page === "Calculators" && <Calculators state={s} onBuy={(saved) => { setBuyPrefill(saved); go("Should I buy this"); }} onSetup={go} />}
          {page === "Categories" && <Categories state={s} onBack={() => go("Calculators")} onAction={act} onRules={() => go("Sorting rules")} />}
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
                    // Text that starts with =, +, - or @ can run as a formula in spreadsheets, so it's prefixed.
                    const cell = (v: string) => `"${v.replace(/"/g, '""').replace(/^[=+@-]/, "'$&")}"`;
                    const csv = [
                      "Date,Merchant,Amount,Account,Category,Subcategory,Note,Status",
                      ...rows.map((t) =>
                        [
                          t.date,
                          cell(t.merchant),
                          (t.direction === "out" ? -t.amount : t.amount) / 100,
                          cell(s.accounts.find((a) => a.id === t.accountId)?.name || ""),
                          cell(t.ignored ? "ignored" : t.splits ? "split" : t.category ? s.categories[t.category] : t.incomeStream || ""),
                          cell(subName(t.subcategoryId) || ""),
                          cell(t.note || ""),
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
                {["all", "expenses", "spending", "savings", "income", "pending"].map((value) => <button key={value} data-h="tick" className={filter === value ? "active" : ""} onClick={() => setFilter(value)}>{value === "all" ? "all" : value === "pending" ? "needs review" : value === "income" ? "income" : s.categories[value as Category]}</button>)}
              </div>
              <div className="history-filter-buttons">
                <button data-h="sheet" onClick={() => setHistoryFiltersOpen(true)}>{accountFilter === "all" ? "all accounts" : s.accounts.find(a => a.id === accountFilter)?.name.toLowerCase()} <ChevronDown size={14} /></button>
                <button data-h="sheet" onClick={() => setHistoryFiltersOpen(true)}>{monthName(historyDate)} <ChevronDown size={14} /></button>
              </div>
              <p className="history-summary">{rows.length} {rows.length === 1 ? "transaction" : "transactions"} · {money(rows.filter(t => t.direction === "out" && !t.ignored).reduce((sum, t) => sum + net(t), 0))} out · {money(rows.filter(t => t.direction === "in" && !t.ignored && !s.links.some(l => l.creditId === t.id)).reduce((sum, t) => sum + t.amount, 0))} in{rows.some(t => t.ignored) ? ` · ${rows.filter(t => t.ignored).length} ignored` : ""}</p>
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
                <div className="history-sheet-chips">{historyMonths.map(m => <button key={m} data-h="tick" className={historyDate === m ? "active" : ""} onClick={() => setHistoryDate(m)}>{monthName(m)}</button>)}<button data-h="tick" className={historyDate === "all time" ? "active" : ""} onClick={() => setHistoryDate("all time")}>all time</button></div>
                <div className="history-sheet-actions"><button data-h="soft" onClick={() => { setFilter("all"); setAccountFilter("all"); setHistoryDate(monthKey(new Date())); setSearch(""); }}>clear</button><button data-h="success" onClick={historySheetDrag.close}>show {rows.length}</button></div>
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
                        <span>{s.accounts.find((a) => a.id === current.accountId)?.name?.toLowerCase() || "account"}{s.accounts.find((a) => a.id === current.accountId)?.mask ? ` · ${s.accounts.find((a) => a.id === current.accountId)?.mask}` : ""}</span>
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
                            <option value="" disabled>choose one…</option>
                            <option value="salary">regular salary</option>
                            <option value="self-employed">
                              self-employment income
                            </option>
                            <option value="other">
                              other income (interest, refunds, gifts)
                            </option>
                            <option value="transfer">
                              transfer between my accounts
                            </option>
                          </select>
                          {current.suggestionNote && <small className="review-note">{current.suggestionNote}</small>}
                          {stream === "other" && !current.suggestionNote && <small className="review-note">other income is tracked but doesn’t raise your safe-to-spend plan.</small>}
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
                      ) : current.suggestedIgnore && !reviewChosen ? (
                        <div className="review-suggestion review-suggestion-ignore"><span>we think it’s</span><strong>not spending</strong><small>{current.suggestionNote || current.suggestedIgnore}. confirming leaves it out of your budget.</small></div>
                      ) : <div className="review-suggestion"><span>we think it’s</span><strong>{(s.subcategories || []).find(c => c.id === selectedSubcategoryId)?.name || s.categories[selected]}</strong><small>{(s.subcategories || []).find(c => c.id === selectedSubcategoryId) ? `in ${s.categories[selected]}` : "you can change this"}{!reviewChosen && current.suggestionNote ? ` · ${current.suggestionNote}` : ""}</small></div>}
                      {sameMerchant(current) > 0 && !(current.suggestedIgnore && !reviewChosen && current.direction === "out") && (
                        <label className="account-toggle review-all" onPointerDown={(e) => e.stopPropagation()}>
                          <span>also sort the other {sameMerchant(current)} from {current.merchant.toLowerCase()}</span>
                          <button type="button" role="switch" aria-checked={reviewAll} data-h="toggle" className={`design-rule-switch ${reviewAll ? "on" : ""}`} onClick={() => setReviewAll(!reviewAll)}><span /></button>
                        </label>
                      )}
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
                        {(s.subcategories || []).some(c => c.group === selected) && <><span className="design-sheet-label">more specific</span><div className="design-sheet-chips"><button data-h="tick" className={!selectedSubcategoryId ? "active" : ""} onClick={() => setReviewSubcategoryId("")}>just {s.categories[selected]}</button>{(s.subcategories || []).filter(c => c.group === selected).map(c => <button key={c.id} data-h="tick" className={selectedSubcategoryId === c.id ? "active" : ""} onClick={() => setReviewSubcategoryId(c.id)}>{c.name}</button>)}</div></>}
                        <button className="design-sheet-save" data-h="success" onClick={reviewSheetDrag.close}>use this category</button>
                        <div className="design-sheet-extras">
                          {current.direction === "out" && (
                            <button data-h="tap" onClick={() => { setReviewPicker(false); setActiveTxId(current.id); setSplitAmounts({ expenses: 0, spending: 0, savings: 0, [current.suggested || "spending"]: current.amount / 100 } as Record<Category, number>); setSplitSheet(true); }}>split it</button>
                          )}
                          <button data-h="tap" onClick={() => { setReviewPicker(false); setActiveTxId(current.id); setIgnoreReason(""); setIgnoreAlways(false); setIgnoreSheet(true); }}>ignore</button>
                        </div>
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
            <RecurringPage
              state={s}
              onAction={act}
              onEdit={recurringEdit}
              onHistory={(merchant) => {
                go("Transactions");
                setSearch(merchant);
                setHistoryDate("all time");
              }}
            />
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
                <div className="monthly-setting savings-stepper">
                  <div>
                    <span>monthly contribution</span>
                    <strong>{money(s.goal.monthly)}</strong>
                  </div>
                  <div className="savings-stepper-controls">
                    <button
                      type="button"
                      aria-label="decrease monthly contribution"
                      disabled={s.goal.monthly < 500}
                      onClick={() => act({ type: "goal", goal: { ...s.goal, monthly: Math.max(0, s.goal.monthly - 2500) } }, "Monthly contribution updated.")}
                    >
                      −
                    </button>
                    <button
                      type="button"
                      aria-label="increase monthly contribution"
                      onClick={() => act({ type: "goal", goal: { ...s.goal, monthly: s.goal.monthly + 2500 } }, "Monthly contribution updated.")}
                    >
                      +
                    </button>
                  </div>
                </div>
                <p className="footnote">
                  this contribution is already set aside in your safe-to-spend
                  amount. actual savings comes from confirmed transactions in
                  your savings category.
                </p>
                <button
                  className="button primary full"
                  onClick={() => {
                    setDepositAmount(2500);
                    setDepositLabel("");
                    setDepositAccountId(s.accounts[0]?.id || "");
                    setDepositSheet(true);
                  }}
                >
                  <Plus size={17} />
                  add money
                </button>
              </section>
              <div className="savings-side">
                <section className="card savings-chart">
                  <div className="section-title">
                    <h2>by month</h2>
                    <span className="muted">last 6 months</span>
                  </div>
                  <div className="savings-bars">
                    {savingsByMonth.map((m) => (
                      <div className="savings-bar" key={m.key}>
                        <i
                          style={{ height: `${Math.max(4, (m.total / savingsMonthMax) * 100)}%` }}
                          data-active={m.total > 0}
                        />
                        <span>{m.label.toLowerCase()}</span>
                      </div>
                    ))}
                  </div>
                </section>
                <section className="card savings-deposits">
                  <div className="section-title">
                    <h2>deposits</h2>
                    <span className="muted">{savingsDeposits.length}</span>
                  </div>
                  {savingsDeposits.length ? (
                    savingsDeposits.slice(0, 8).map((t) => (
                      <div className="savings-deposit-row" key={t.id}>
                        <span>{t.merchant.toLowerCase()}</span>
                        <span className="muted">{dateLabel(t.date)}</span>
                        <strong>{money(savingsAmount(t), true)}</strong>
                        {t.manual && (
                          <button
                            type="button"
                            className="deposit-remove"
                            aria-label={`remove ${t.merchant} on ${dateLabel(t.date)}`}
                            onClick={() => confirmFirst(`remove ${money(t.amount, true)}?`, `“${t.merchant.toLowerCase()}” on ${dateLabel(t.date).toLowerCase()} will no longer count toward your goal.`, "remove", () => act({ type: "delete-transaction", transactionId: t.id }, "Deposit removed."))}
                          >
                            <X size={14} />
                          </button>
                        )}
                      </div>
                    ))
                  ) : (
                    <p className="footnote">
                      no deposits yet. confirmed savings transactions and
                      manual deposits will show up here.
                    </p>
                  )}
                </section>
                <aside className="review-aside">
                  <Sprout size={26} />
                  <h3>slow is still forward.</h3>
                  <p>you don’t need a perfect month to make a little progress.</p>
                  <p>
                    your goal is a plan, not an automatic bank transfer. you
                    stay in control.
                  </p>
                </aside>
              </div>
              {depositSheet && (
                <div className="design-sheet-backdrop" data-closing={depositSheetDrag.closing} onClick={depositSheetDrag.close}>
                  <div className="design-sheet" role="dialog" aria-modal="true" aria-label="add money to your goal" data-dragging={depositSheetDrag.dragging} style={depositSheetDrag.style} {...depositSheetDrag.handlers} onClick={(event) => event.stopPropagation()}>
                    <div className="history-sheet-handle" />
                    <div className="design-sheet-title">
                      <h2>add money</h2>
                      <button aria-label="close" onClick={depositSheetDrag.close}><X size={20} /></button>
                    </div>
                    <label className="design-sheet-amount deposit-amount">
                      <span aria-hidden="true">$</span>
                      <input
                        type="number"
                        inputMode="decimal"
                        min="0.01"
                        max="100000000"
                        step="0.01"
                        aria-label="amount in dollars"
                        value={depositAmount ? depositAmount / 100 : ""}
                        onChange={(e) => setDepositAmount(Math.max(0, Math.round((Number(e.target.value) || 0) * 100)))}
                      />
                    </label>
                    <div className="design-sheet-chips">
                      {[2500, 5000, 10000, 25000].map((amount) => (
                        <button key={amount} data-h="tick" className={depositAmount === amount ? "active" : ""} onClick={() => setDepositAmount(amount)}>{money(amount)}</button>
                      ))}
                    </div>
                    <label className="design-sheet-field">label<input maxLength={80} value={depositLabel} onChange={(e) => setDepositLabel(e.target.value)} placeholder="added to savings" /></label>
                    <span className="design-sheet-label">from</span>
                    <div className="design-sheet-chips">
                      {s.accounts.map((a) => (
                        <button key={a.id} data-h="tick" className={depositAccountId === a.id ? "active" : ""} onClick={() => setDepositAccountId(a.id)}>{a.name.toLowerCase()}</button>
                      ))}
                    </div>
                    <button
                      className="design-sheet-save"
                      data-h="success"
                      disabled={!depositAccountId || depositAmount <= 0}
                      onClick={async () => {
                        if (await act({ type: "add-savings", amount: depositAmount, fromAccountId: depositAccountId, note: depositLabel.trim() || undefined, date: new Date().toLocaleDateString("en-CA") }, "Added to your goal."))
                          depositSheetDrag.close();
                      }}
                    >
                      add {money(depositAmount, true)}
                    </button>
                    <p className="design-sheet-help">records money you set aside yourself. if this transfer will also show up from a connected bank, confirm that one as savings instead so it isn’t counted twice.</p>
                  </div>
                </div>
              )}
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
              {!s.demo && (
                <section className={`card simplefin-card ${s.connection?.connected ? "" : "empty-connection"}`}>
                  <div>
                    <strong>{s.connection?.connected ? "connected through simplefin" : "no bank connection yet"}</strong>
                    <p>
                      {s.connection?.connected
                        ? `${s.connection.lastFetch ? `last checked ${new Date(s.connection.lastFetch).toLocaleString("en-US", { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" }).toLowerCase()}` : "not checked yet"} · banks refresh about once a day, and sofar checks every few hours.`
                        : "connect your banks with a simplefin setup token to bring in balances and transactions."}
                    </p>
                    {s.connection?.error && <p className="connection-error"><CloudOff size={14} /> {s.connection.error}</p>}
                  </div>
                  <div className="simplefin-actions">
                    {s.connection?.connected ? (
                      <>
                        <a className="button small-button" href={s.connection.bridgeUrl || "https://bridge.simplefin.org"} target="_blank" rel="noreferrer">manage banks <ArrowUpRight size={15} /></a>
                        <button className="button small-button" onClick={openConnect}>use a new token</button>
                        <button className="text-button account-disconnect" onClick={disconnectSimplefin}>disconnect</button>
                      </>
                    ) : (
                      <button className="button primary small-button" onClick={openConnect}>connect</button>
                    )}
                  </div>
                </section>
              )}
              {(["depository", "credit", "investment"] as const).map((type) => {
                const group = s.accounts.filter((a) => a.type === type);
                if (!group.length) return null;
                const thisMonth = `${new Date().getFullYear()}-${String(new Date().getMonth() + 1).padStart(2, "0")}`;
                return (
                  <div className="account-group" key={type}>
                    <div className="account-group-heading">
                      <h2>{type === "depository" ? "banking" : type === "credit" ? "credit cards" : "investing"}</h2>
                      <span className="muted">{money(group.reduce((n, a) => n + a.balance, 0), true)}</span>
                    </div>
                    <div className="account-grid">
                      {group.map((a) => {
                        const expanded = expandedAccount === a.id;
                        const monthCount = s.transactions.filter((t) => t.accountId === a.id && t.date.slice(0, 7) === thisMonth).length;
                        return (
                          <section className={`card account-card ${expanded ? "expanded" : ""}`} key={a.id}>
                            <button
                              type="button"
                              className="account-card-header"
                              aria-expanded={expanded}
                              onClick={() => setExpandedAccount(expanded ? null : a.id)}
                            >
                              <span className={`bank-logo bank-${a.institution.charCodeAt(0) % 3}`}>{a.institution.charAt(0)}</span>
                              <span className="account-card-heading">
                                <strong>{a.name}</strong>
                                <small>{a.institution}{a.mask ? ` · •••• ${a.mask}` : ""}</small>
                              </span>
                              {a.needsReauth ? <span className="pill pending">needs attention</span> : <span className="pill income">connected</span>}
                            </button>
                            <strong className="account-balance">{money(a.balance, true)}</strong>
                            {a.needsReauth && (
                              <div className="account-reauth">
                                <CloudOff size={16} />
                                <span>{a.problem || `${a.institution.toLowerCase()} needs you to sign in again in simplefin bridge.`} balances may be out of date.</span>
                                {s.demo ? (
                                  <button className="button small-button" onClick={() => act({ type: "account-settings", accountId: a.id, clearReauth: true }, "Fixed. In your own sofar you'd do this in SimpleFIN Bridge.")}>fix it</button>
                                ) : (
                                  <a className="button small-button" href={s.connection?.bridgeUrl || "https://bridge.simplefin.org"} target="_blank" rel="noreferrer">fix in simplefin <ArrowUpRight size={14} /></a>
                                )}
                              </div>
                            )}
                            <div className="account-foot">
                              <span className="status-dot" />
                              last synced{" "}
                              {a.syncedAt ? new Date(a.syncedAt).toLocaleDateString() : "—"}
                            </div>
                            {expanded && (
                              <div className="account-detail">
                                <span className="account-detail-count">{monthCount} transaction{monthCount === 1 ? "" : "s"} this month</span>
                                <label className="account-toggle">
                                  <span>counts toward safe to spend</span>
                                  <button
                                    type="button"
                                    data-h="toggle"
                                    className={`design-rule-switch ${a.excludedFromSafeToSpend ? "" : "on"}`}
                                    onClick={() =>
                                      act(
                                        { type: "account-settings", accountId: a.id, excluded: !a.excludedFromSafeToSpend },
                                        a.excludedFromSafeToSpend ? "Included in safe to spend." : "Excluded from safe to spend.",
                                      )
                                    }
                                  >
                                    <span />
                                  </button>
                                </label>
                                <label className="account-rename">
                                  <span>name in sofar</span>
                                  <input
                                    key={a.name}
                                    defaultValue={a.name}
                                    maxLength={60}
                                    aria-label={`rename ${a.name}`}
                                    onKeyDown={(e) => { if (e.key === "Enter") e.currentTarget.blur(); }}
                                    onBlur={(e) => {
                                      const name = e.currentTarget.value.trim();
                                      if (name !== a.name) act({ type: "account-settings", accountId: a.id, name }, name ? "Account renamed." : "Using the bank's name.");
                                    }}
                                  />
                                </label>
                                <label className="account-rename">
                                  <span>account type</span>
                                  <select
                                    value={a.type === "depository" ? (a.subtype === "savings" ? "savings" : "checking") : a.type}
                                    onChange={(e) => act({ type: "account-settings", accountId: a.id, accountType: e.target.value }, "Account type updated.")}
                                  >
                                    <option value="checking">checking</option>
                                    <option value="savings">savings</option>
                                    <option value="credit">credit card</option>
                                    <option value="investment">investment (balance only)</option>
                                  </select>
                                </label>
                                <button type="button" className="account-disconnect" onClick={() => removeAccount(a)}>
                                  remove from sofar
                                </button>
                              </div>
                            )}
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
                        );
                      })}
                    </div>
                  </div>
                );
              })}
              <button className="account-add" onClick={openConnect}>
                <span>
                  <Plus size={27} />
                </span>
                <h3>{s.connection?.connected ? "add another bank" : "connect your banks"}</h3>
                <p>{s.connection?.connected ? "banks are added in simplefin bridge." : "checking, savings, credit cards, or investments."}</p>
              </button>
              <div className="info-box">
                <ShieldCheck size={21} />
                <span>
                  your bank login stays with simplefin. sofar gets read-only
                  access and can’t move money.
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
                  <div className="segmented" role="radiogroup" aria-label="motion style" data-active={motionMode === "expressive" ? 0 : 1}>
                    <button type="button" role="radio" aria-checked={motionMode === "expressive"} data-h="tick" className={motionMode === "expressive" ? "active" : ""} onClick={() => { setMotionMode("expressive"); localStorage.setItem("sofar-motion", "expressive"); }}>on</button>
                    <button type="button" role="radio" aria-checked={motionMode === "off"} data-h="tick" className={motionMode === "off" ? "active" : ""} onClick={() => { setMotionMode("off"); localStorage.setItem("sofar-motion", "off"); }}>off</button>
                  </div>
                </div>
                <div className="setting-line">
                  <div><strong>touch feedback</strong><p>distinct pulses for presses, choices, swipes, sheets, and confirmations on supported devices. visual ripples show feedback elsewhere.</p></div>
                  <span className="setting-actions">
                    <button className="button small-button" type="button" data-h="success" disabled={!hapticsEnabled} aria-label="test touch feedback">feel it</button>
                    <div className="segmented" role="radiogroup" aria-label="touch feedback" data-active={hapticsEnabled ? 0 : 1}>
                      <button type="button" role="radio" aria-checked={hapticsEnabled} data-h="manual" className={hapticsEnabled ? "active" : ""} onClick={event => { if (hapticsEnabled) return; setHapticEnabled(true); emitHaptic("toggle", { x: event.clientX, y: event.clientY }); setHapticsEnabled(true); localStorage.setItem("sofar-haptics", "on"); }}>on</button>
                      <button type="button" role="radio" aria-checked={!hapticsEnabled} data-h="manual" className={!hapticsEnabled ? "active" : ""} onClick={event => { if (!hapticsEnabled) return; emitHaptic("toggleOff", { x: event.clientX, y: event.clientY }); setHapticsEnabled(false); setHapticEnabled(false); localStorage.setItem("sofar-haptics", "off"); }}>off</button>
                    </div>
                  </span>
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
                    <button
                      className="button small-button"
                      onClick={() => setModal(
                        <TOTPDisableForm
                          onDone={() => {
                            setModal(null);
                            setTotpEnabled(false);
                            setNotice("Two-step verification is off.");
                          }}
                        />,
                      )}
                    >
                      turn off
                    </button>
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
                {!s.demo && (
                  <div className="setting-line">
                    <div>
                      <strong>password</strong>
                      <p>changing it signs out your other devices.</p>
                    </div>
                    <button
                      className="button small-button"
                      onClick={() => setModal(
                        <PasswordForm
                          onDone={() => {
                            setModal(null);
                            setNotice("Password changed. Other devices were signed out.");
                          }}
                        />,
                      )}
                    >
                      change
                    </button>
                  </div>
                )}
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
      {overlays}
      {splitSheet && activeTx && (
        <div className="design-sheet-backdrop" data-closing={splitSheetDrag.closing} onClick={splitSheetDrag.close}><div className="design-sheet" role="dialog" aria-modal="true" aria-label="split this transaction" data-dragging={splitSheetDrag.dragging} style={splitSheetDrag.style} {...splitSheetDrag.handlers} onClick={event => event.stopPropagation()}>
          <div className="history-sheet-handle" /><div className="design-sheet-title"><h2>split {activeTx.merchant.toLowerCase()}</h2><button aria-label="close" onClick={splitSheetDrag.close}><X size={20} /></button></div>
          <p className="design-sheet-help">divide {money(activeTx.amount, true)} across categories.</p>
          <div className="split-rows">
            {categories.map(c => (
              <label className="split-row" key={c}>
                <span>{s.categories[c]}</span>
                <span className="split-input"><span>$</span><input type="number" min="0" step="0.01" value={splitAmounts[c] || ""} onChange={e => setSplitAmounts({ ...splitAmounts, [c]: Number(e.target.value) })} /></span>
              </label>
            ))}
          </div>
          {(() => {
            const remainder = activeTx.amount - Math.round(categories.reduce((n, c) => n + (splitAmounts[c] || 0) * 100, 0));
            const partsUsed = categories.filter(c => (splitAmounts[c] || 0) > 0).length;
            return (
              <>
                <p className={`split-remainder ${remainder === 0 ? "ok" : ""}`}>{remainder === 0 ? "adds up." : `${money(Math.abs(remainder), true)} ${remainder > 0 ? "left to place" : "over"}`}</p>
                <button
                  className="design-sheet-save"
                  data-h="success"
                  disabled={remainder !== 0 || partsUsed < 2}
                  onClick={async () => {
                    const splits = categories.filter(c => (splitAmounts[c] || 0) > 0).map(c => ({ category: c, amount: Math.round((splitAmounts[c] || 0) * 100) }));
                    if (await act({ type: "split", transactionId: activeTx.id, splits }, "Split across categories.")) {
                      splitSheetDrag.close();
                      setModal(null);
                    }
                  }}
                >
                  save split
                </button>
              </>
            );
          })()}
        </div></div>
      )}
      {ignoreSheet && activeTx && (
        <div className="design-sheet-backdrop" data-closing={ignoreSheetDrag.closing} onClick={ignoreSheetDrag.close}><div className="design-sheet" role="dialog" aria-modal="true" aria-label="ignore this transaction" data-dragging={ignoreSheetDrag.dragging} style={ignoreSheetDrag.style} {...ignoreSheetDrag.handlers} onClick={event => event.stopPropagation()}>
          <div className="history-sheet-handle" /><div className="design-sheet-title"><h2>why ignore it?</h2><button aria-label="close" onClick={ignoreSheetDrag.close}><X size={20} /></button></div>
          <div className="design-sheet-chips">
            {["transfer", "card payment", "not mine", "duplicate", "refunded", "other"].map(reason => (
              <button key={reason} data-h="tick" className={ignoreReason === reason ? "active" : ""} onClick={() => setIgnoreReason(reason)}>{reason}</button>
            ))}
          </div>
          <label className="account-toggle">
            <span>always ignore {activeTx.merchant.toLowerCase()}{sameMerchant(activeTx) > 0 ? `, including ${sameMerchant(activeTx)} waiting for review` : ""}</span>
            <button type="button" data-h="toggle" className={`design-rule-switch ${ignoreAlways ? "on" : ""}`} onClick={() => setIgnoreAlways(!ignoreAlways)}><span /></button>
          </label>
          <button
            className="design-sheet-save"
            data-h="success"
            disabled={!ignoreReason}
            onClick={async () => {
              if (await act({ type: "ignore", transactionId: activeTx.id, reason: ignoreReason, always: ignoreAlways }, "Ignored. It won't count toward your budget.")) {
                ignoreSheetDrag.close();
                setModal(null);
              }
            }}
          >
            ignore this transaction
          </button>
        </div></div>
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
function BillForm({
  names,
  onSave,
}: {
  names: State["categories"];
  onSave: (bill: { merchant: string; amount: number; cadence: Recurring["cadence"]; nextDate: string; category: Category; tolerance: number }) => void;
}) {
  const soon = new Date();
  soon.setDate(soon.getDate() + 7);
  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        const d = new FormData(e.currentTarget);
        onSave({
          merchant: String(d.get("merchant")).trim(),
          amount: Math.round(Number(d.get("amount")) * 100),
          cadence: String(d.get("cadence")) as Recurring["cadence"],
          nextDate: String(d.get("nextDate")),
          category: String(d.get("category")) as Category,
          tolerance: Number(d.get("tolerance")),
        });
      }}
    >
      <span className="modal-symbol">
        <Repeat2 />
      </span>
      <h2>add a bill</h2>
      <p>something you pay on a schedule. it’s set aside before your safe-to-spend number, and sofar matches it when it posts.</p>
      <label className="field">
        name
        <input name="merchant" required maxLength={80} placeholder="e.g. rent, phone, gym" />
        <small>use part of the name your bank shows, so sofar can match it.</small>
      </label>
      <label className="field">
        amount ($)
        <input name="amount" type="number" min="0.01" max="100000000" step="0.01" required inputMode="decimal" />
      </label>
      <label className="field">
        how often
        <select name="cadence" defaultValue="monthly">
          <option value="weekly">weekly</option>
          <option value="biweekly">every two weeks</option>
          <option value="monthly">monthly</option>
          <option value="annual">yearly</option>
        </select>
      </label>
      <label className="field">
        next due
        <input name="nextDate" type="date" required defaultValue={soon.toLocaleDateString("en-CA")} />
      </label>
      <label className="field">
        category
        <select name="category" defaultValue="expenses">
          {categories.map((c) => (
            <option key={c} value={c}>{names[c]}</option>
          ))}
        </select>
        <small>only bills in {names.expenses} are set aside before safe to spend.</small>
      </label>
      <label className="field">
        amount tolerance (%)
        <input name="tolerance" type="number" min="0" max="100" step="0.1" required defaultValue={10} />
        <small>we’ll flag a charge that differs by more than this.</small>
      </label>
      <button className="button primary full">
        add bill
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
      {item.type === "income" ? (
        <input type="hidden" name="category" value={item.category || "spending"} />
      ) : (
        <label className="field">
          category
          <select name="category" defaultValue={item.category}>
            {categories.map((c) => (
              <option key={c} value={c}>
                {names[c]}
              </option>
            ))}
          </select>
          <small>bills in {names.expenses} are set aside before safe to spend.</small>
        </label>
      )}
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
  const [qr, setQr] = useState("");
  useEffect(() => {
    QRCode.toDataURL(url, { margin: 1, width: 220, color: { dark: "#24332b", light: "#ffffff" } })
      .then(setQr)
      .catch(() => setQr(""));
  }, [url]);
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
        scan this with your authenticator app, or enter the secret by hand
        as a time-based code. then enter the six-digit code it shows.
      </p>
      {qr && <img className="totp-qr" src={qr} width={220} height={220} alt="QR code for your authenticator app" />}
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
function PasswordForm({ onDone }: { onDone: () => void }) {
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        const d = new FormData(e.currentTarget);
        if (d.get("next") !== d.get("confirm")) {
          setError("The new passwords don’t match.");
          return;
        }
        setBusy(true);
        try {
          await api("/auth/password", { current: d.get("current"), next: d.get("next") });
          onDone();
        } catch (e) {
          setError((e as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <span className="modal-symbol">
        <ShieldCheck />
      </span>
      <h2>change your password</h2>
      <label className="field">
        current password
        <input name="current" type="password" required autoComplete="current-password" />
      </label>
      <label className="field">
        new password
        <input name="next" type="password" required minLength={12} maxLength={72} autoComplete="new-password" />
        <small>at least 12 characters.</small>
      </label>
      <label className="field">
        new password again
        <input name="confirm" type="password" required minLength={12} maxLength={72} autoComplete="new-password" />
      </label>
      {error && <p className="error" role="alert">{error}</p>}
      <button className="button primary full" disabled={busy}>
        {busy ? <Loader2 className="spin" size={18} /> : "change password"}
      </button>
    </form>
  );
}
function TOTPDisableForm({ onDone }: { onDone: () => void }) {
  const [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        const d = new FormData(e.currentTarget);
        setBusy(true);
        try {
          await api("/auth/totp/disable", { password: d.get("password"), code: d.get("code") });
          onDone();
        } catch (e) {
          setError((e as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <span className="modal-symbol">
        <ShieldCheck />
      </span>
      <h2>turn off two-step verification?</h2>
      <p>sign-in will only need your password. confirm it’s you first.</p>
      <label className="field">
        password
        <input name="password" type="password" required autoComplete="current-password" />
      </label>
      <label className="field">
        six-digit code
        <input name="code" required pattern="[0-9]{6}" inputMode="numeric" autoComplete="one-time-code" maxLength={6} />
      </label>
      {error && <p className="error" role="alert">{error}</p>}
      <button className="button primary full danger" disabled={busy}>
        {busy ? <Loader2 className="spin" size={18} /> : "turn it off"}
      </button>
    </form>
  );
}
function SimpleFinForm({
  demo,
  connected,
  bridgeUrl,
  onConnected,
}: {
  demo: boolean;
  connected: boolean;
  bridgeUrl?: string;
  onConnected: (warning?: string) => Promise<void>;
}) {
  const [token, setToken] = useState(""),
    [error, setError] = useState(""),
    [busy, setBusy] = useState(false);
  const bridge = bridgeUrl || "https://bridge.simplefin.org";
  if (demo)
    return (
      <div>
        <span className="modal-symbol">
          <Landmark />
        </span>
        <h2>connect your banks</h2>
        <p>
          your own sofar connects through simplefin bridge, a $15/year service
          that gives read-only access to your banks. this demo uses sample
          data, so there’s nothing to connect here.
        </p>
        <div className="info-box">
          <ShieldCheck size={20} />
          <span>run your own server (see the readme), then paste a simplefin setup token here.</span>
        </div>
      </div>
    );
  return (
    <form
      onSubmit={async (e) => {
        e.preventDefault();
        setBusy(true);
        setError("");
        try {
          const result = await api("/simplefin/connect", { token: token.trim() });
          await onConnected(result.warning);
        } catch (err) {
          setError((err as Error).message);
        } finally {
          setBusy(false);
        }
      }}
    >
      <span className="modal-symbol">
        <Landmark />
      </span>
      <h2>{connected ? "add or fix a bank" : "connect your banks"}</h2>
      {connected ? (
        <p>
          banks are added and reconnected in{" "}
          <a href={bridge} target="_blank" rel="noreferrer">simplefin bridge</a>.
          sofar picks up changes on its next check. only paste a new setup
          token if you want to replace sofar’s access.
        </p>
      ) : (
        <ol className="connect-steps">
          <li>
            open <a href={bridge} target="_blank" rel="noreferrer">simplefin bridge <ArrowUpRight size={13} /></a>{" "}
            and connect your banks there ($15/year).
          </li>
          <li>create a new app connection and copy its setup token.</li>
          <li>paste it below. each token works once.</li>
        </ol>
      )}
      <label className="field">
        setup token
        <textarea
          className="token-input"
          name="token"
          required
          rows={3}
          autoComplete="off"
          spellCheck={false}
          value={token}
          onChange={(e) => setToken(e.target.value)}
        />
        <small>sofar stores the access it receives encrypted on your server.</small>
      </label>
      {error && <p className="error" role="alert">{error}</p>}
      <button className="button primary full" disabled={busy || !token.trim()}>
        {busy ? <><Loader2 className="spin" size={18} /> bringing in your history…</> : connected ? "replace access" : "connect"}
      </button>
    </form>
  );
}
function CategoryPicker({
  state,
  current,
  onPick,
}: {
  state: State;
  current: Transaction;
  onPick: (category: Category, subcategoryId?: string) => void;
}) {
  const [group, setGroup] = useState<Category>(current.category || current.suggested || "spending");
  const subs = (state.subcategories || []).filter((c) => c.group === group);
  return (
    <div className="category-picker">
      <div className="category-groups" role="tablist" aria-label="category group">
        {categories.map((c) => (
          <button key={c} role="tab" aria-selected={group === c} className={group === c ? "active" : ""} onClick={() => setGroup(c)}>
            {state.categories[c]}
          </button>
        ))}
      </div>
      <div className="category-subs">
        <button
          className={`category-option ${current.category === group && !current.subcategoryId ? "selected" : ""}`}
          onClick={() => onPick(group)}
        >
          just {state.categories[group]}
        </button>
        {subs.map((c) => (
          <button key={c.id} className={`category-option ${current.subcategoryId === c.id ? "selected" : ""}`} onClick={() => onPick(group, c.id)}>
            {c.name}
          </button>
        ))}
      </div>
    </div>
  );
}
