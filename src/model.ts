export type Category = "expenses" | "spending" | "savings";
export type Transaction = {
  id: string;
  accountId: string;
  date: string;
  amount: number;
  merchant: string;
  category: Category | null;
  status: "pending" | "confirmed";
  direction: "in" | "out";
  incomeStream?: "salary" | "self-employed" | "transfer";
  suggested?: Category;
  subcategoryId?: string;
  suggestedSubcategoryId?: string;
  recurringId?: string;
  bankPending?: boolean;
  note?: string;
  ignored?: boolean;
  ignoreReason?: string;
  splits?: { category: Category; subcategoryId?: string; amount: number }[];
};
export type Account = {
  id: string;
  name: string;
  institution: string;
  type: string;
  subtype: string;
  mask: string;
  balance: number;
  syncedAt: string;
  excludedFromSafeToSpend?: boolean;
  needsReauth?: boolean;
};
export type Recurring = {
  id: string;
  merchant: string;
  amount: number;
  tolerance: number;
  cadence: "weekly" | "biweekly" | "monthly" | "annual";
  type: "bill" | "income";
  incomeStream?: "salary" | "self-employed";
  category: Category;
  confirmed: boolean;
  dismissed?: boolean;
  threshold: number;
  nextDate: string;
  mismatch?: boolean;
};
export type Subcategory = { id: string; name: string; group: Category; monthlyPlan: number };
export type State = {
  transactions: Transaction[];
  accounts: Account[];
  recurring: Recurring[];
  rules: { pattern: string; category: Category; subcategoryId?: string; enabled?: boolean }[];
  subcategories?: Subcategory[];
  links: { id: string; expenseId: string; creditId: string; amount: number }[];
  goal: { name: string; target: number; monthly: number; saved: number };
  categories: Record<Category, string>;
  threshold: number;
  lastSync: string;
  demo: boolean;
  ignoreRules: { pattern: string }[];
};
export type Action = {
  id: string;
  type: string;
  transactionId?: string;
  category?: Category;
  incomeStream?: string;
  expenseId?: string;
  creditId?: string;
  amount?: number;
  linkId?: string;
  recurringId?: string;
  tolerance?: number;
  goal?: State["goal"];
  threshold?: number;
  categories?: State["categories"];
  subcategory?: Subcategory;
  subcategoryId?: string;
  pattern?: string;
  oldPattern?: string;
  enabled?: boolean;
  note?: string;
  reason?: string;
  always?: boolean;
  splits?: Transaction["splits"];
  accountId?: string;
  excluded?: boolean;
  clearReauth?: boolean;
  fromAccountId?: string;
  date?: string;
};
export const money = (c: number, decimals = false) =>
  new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: decimals ? 2 : 0,
    maximumFractionDigits: decimals ? 2 : 0,
  }).format(c / 100);
export const dailyAllowance = (monthlyCents: number, period: Date) =>
  Math.round(
    monthlyCents /
      new Date(period.getFullYear(), period.getMonth() + 1, 0).getDate(),
  );
export const normalize = (s: string) =>
  s
    .toLowerCase()
    .replace(/\.com\b/g, "")
    .replace(/[^a-z ]/g, " ")
    .replace(/\s+/g, " ")
    .trim();
export function budget(s: State, now = new Date()) {
  const end = new Date(now.getFullYear(), now.getMonth(), 1);
  const start = new Date(now.getFullYear(), now.getMonth() - 3, 1);
  const excludedAccounts = new Set(
    s.accounts.filter((a) => a.excludedFromSafeToSpend).map((a) => a.id),
  );
  let salary = 0,
    selfEmployed = 0;
  for (const t of s.transactions) {
    const d = new Date(t.date + "T12:00:00");
    if (
      t.status !== "confirmed" ||
      t.bankPending ||
      t.ignored ||
      t.direction !== "in" ||
      d < start ||
      d >= end ||
      excludedAccounts.has(t.accountId) ||
      s.links.some((l) => l.creditId === t.id)
    )
      continue;
    if (t.incomeStream === "salary") salary += t.amount;
    if (t.incomeStream === "self-employed") selfEmployed += t.amount;
  }
  salary = Math.round(salary / 3);
  selfEmployed = Math.round(selfEmployed / 3);
  const bills = s.recurring
    .filter(
      (r) => r.confirmed && !r.dismissed && r.type === "bill" && r.category === "expenses",
    )
    .reduce(
      (n, r) =>
        n +
        Math.round(
          r.amount *
            { weekly: 52 / 12, biweekly: 26 / 12, monthly: 1, annual: 1 / 12 }[
              r.cadence
            ],
        ),
      0,
    );
  const plannedExpenses = (s.subcategories || []).filter(c => c.group === "expenses").reduce((n, c) => n + c.monthlyPlan, 0);
  const plannedSavings = (s.subcategories || []).filter(c => c.group === "savings").reduce((n, c) => n + c.monthlyPlan, 0);
  const totals = { expenses: 0, spending: 0, savings: 0 };
  for (const t of s.transactions) {
    if (
      t.status !== "confirmed" ||
      t.ignored ||
      (!t.category && !t.splits) ||
      t.direction !== "out" ||
      excludedAccounts.has(t.accountId) ||
      t.date.slice(0, 7) !==
        `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, "0")}`
    )
      continue;
    if (t.splits) {
      for (const split of t.splits) totals[split.category] += split.amount;
      continue;
    }
    totals[t.category!] +=
      t.amount -
      s.links
        .filter((l) => l.expenseId === t.id)
        .reduce((n, l) => n + l.amount, 0);
  }
  return {
    salary,
    selfEmployed,
    income: salary + selfEmployed,
    bills,
    safe: salary + selfEmployed - Math.max(bills, plannedExpenses) - Math.max(s.goal.monthly, plannedSavings),
    plannedExpenses,
    plannedSavings,
    totals,
    start,
    end,
  };
}
export function reimbursementMatches(s: State, credit: Transaction) {
  return s.transactions
    .filter(
      (t) =>
        t.direction === "out" &&
        t.status === "confirmed" &&
        t.date <= credit.date &&
        t.amount >
          s.links
            .filter((l) => l.expenseId === t.id)
            .reduce((n, l) => n + l.amount, 0),
    )
    .sort((a, b) => score(a) - score(b));
  function score(t: Transaction) {
    return (
      Math.abs(t.amount - credit.amount) / Math.max(credit.amount, 1) +
      Math.abs(Date.parse(credit.date) - Date.parse(t.date)) / 86400000 / 60
    );
  }
}
export function applyAction(state: State, a: Action): State {
  const s = structuredClone(state);
  const t = s.transactions.find((t) => t.id === a.transactionId);
  const savedTotal = (s: State) =>
    s.transactions
      .filter(
        (t) =>
          t.status === "confirmed" &&
          t.direction === "out" &&
          !t.bankPending &&
          !t.ignored &&
          (t.splits
            ? t.splits.some((sp) => sp.category === "savings")
            : t.category === "savings"),
      )
      .reduce(
        (sum, t) =>
          sum +
          (t.splits
            ? t.splits
                .filter((sp) => sp.category === "savings")
                .reduce((n, sp) => n + sp.amount, 0)
            : t.amount -
              s.links
                .filter((l) => l.expenseId === t.id)
                .reduce((n, l) => n + l.amount, 0)),
        0,
      );
  const previousSaved = savedTotal(s);
  if (a.type === "review" && t) {
    if (!a.category) throw new Error("Choose a category.");
    t.category = a.category;
    t.subcategoryId = a.subcategoryId;
    t.status = "confirmed";
    t.incomeStream = a.incomeStream as Transaction["incomeStream"];
    t.ignored = false;
    t.ignoreReason = undefined;
    t.splits = undefined;
    const pattern = normalize(t.merchant);
    s.rules = s.rules.filter((r) => r.pattern !== pattern);
    s.rules.push({ pattern, category: a.category, subcategoryId: a.subcategoryId, enabled: true });
  }
  if (a.type === "reimburse") {
    const e = s.transactions.find((t) => t.id === a.expenseId),
      c = s.transactions.find((t) => t.id === a.creditId);
    const used = s.links
      .filter((l) => l.expenseId === a.expenseId)
      .reduce((n, l) => n + l.amount, 0);
    if (
      !e ||
      !c ||
      e.direction !== "out" ||
      c.direction !== "in" ||
      s.links.some((l) => l.creditId === c.id) ||
      !a.amount ||
      a.amount > c.amount ||
      a.amount > e.amount - used ||
      a.amount <= 0
    )
      throw new Error(
        "Choose an available expense and a valid repayment amount.",
      );
    s.links.push({
      id: a.id,
      expenseId: e.id,
      creditId: c.id,
      amount: a.amount,
    });
    c.status = "confirmed";
    c.category = e.category;
    c.incomeStream = undefined;
  }
  if (a.type === "unlink") {
    const l = s.links.find((l) => l.id === a.linkId);
    if (l) {
      const c = s.transactions.find((t) => t.id === l.creditId);
      if (c) {
        c.status = "pending";
        c.category = null;
        c.incomeStream = undefined;
      }
    }
    s.links = s.links.filter((l) => l.id !== a.linkId);
  }
  if (a.type === "recurring") {
    const r = s.recurring.find((r) => r.id === a.recurringId);
    if (r) {
      r.confirmed = true;
      if (a.category) r.category = a.category;
      if (a.incomeStream)
        r.incomeStream = a.incomeStream as Recurring["incomeStream"];
      if (a.tolerance !== undefined) r.tolerance = a.tolerance;
      if (a.amount && a.amount > 0) r.amount = a.amount;
      r.mismatch = false;
      r.dismissed = false;
    }
  }
  if (a.type === "dismiss-recurring" || a.type === "restore-recurring") {
    const r = s.recurring.find((r) => r.id === a.recurringId);
    if (!r) throw new Error("Recurring item no longer available.");
    r.dismissed = a.type === "dismiss-recurring";
    r.mismatch = false;
  }
  if (a.type === "goal" && a.goal) s.goal = a.goal;
  if (a.type === "settings") {
    if (a.threshold) s.threshold = a.threshold;
    if (a.categories) s.categories = a.categories;
  }
  if (a.type === "subcategory-upsert" && a.subcategory) {
    const subcategories = s.subcategories || (s.subcategories = []);
    const index = subcategories.findIndex(x => x.id === a.subcategory!.id);
    if (index >= 0 && subcategories[index].group !== a.subcategory.group) {
      for (const tx of s.transactions) if (tx.subcategoryId === a.subcategory.id) tx.category = a.subcategory.group;
      for (const rule of s.rules) if (rule.subcategoryId === a.subcategory.id) rule.category = a.subcategory.group;
    }
    if (index >= 0) subcategories[index] = a.subcategory;
    else subcategories.push(a.subcategory);
  }
  if (a.type === "subcategory-delete" && a.subcategoryId) {
    s.subcategories = (s.subcategories || []).filter(x => x.id !== a.subcategoryId);
    for (const tx of s.transactions) if (tx.subcategoryId === a.subcategoryId) tx.subcategoryId = undefined;
    for (const r of s.rules) if (r.subcategoryId === a.subcategoryId) r.subcategoryId = undefined;
  }
  if (a.type === "rule-upsert" && a.pattern && a.category) {
    s.rules = s.rules.filter(r => r.pattern !== (a.oldPattern || a.pattern) && r.pattern !== a.pattern);
    s.rules.push({ pattern: a.pattern, category: a.category, subcategoryId: a.subcategoryId, enabled: true });
  }
  if (a.type === "rule-backfill" && a.pattern && a.category) {
    for (const tx of s.transactions) {
      if (tx.status === "confirmed" && tx.direction === "out" && !tx.ignored && !tx.splits && normalize(tx.merchant).includes(a.pattern)) {
        tx.category = a.category;
        tx.subcategoryId = a.subcategoryId;
      }
    }
  }
  if (a.type === "rule-delete" && a.pattern) s.rules = s.rules.filter(r => r.pattern !== a.pattern);
  if (a.type === "rule-toggle" && a.pattern) {
    const rule = s.rules.find(r => r.pattern === a.pattern);
    if (rule) rule.enabled = !!a.enabled;
  }
  if (a.type === "ignore" && t) {
    t.status = "confirmed";
    t.ignored = true;
    t.ignoreReason = a.reason;
    t.category = null;
    if (a.always) {
      const pattern = normalize(t.merchant);
      if (!s.ignoreRules.some((r) => r.pattern === pattern))
        s.ignoreRules.push({ pattern });
    }
  }
  if (a.type === "split" && t && a.splits) {
    if (t.direction !== "out") throw new Error("Only spending can be split.");
    if (s.links.some((l) => l.expenseId === t.id))
      throw new Error("Unlink repayments before splitting this expense.");
    if (
      a.splits.length < 2 ||
      a.splits.some((sp) => sp.amount <= 0) ||
      a.splits.reduce((n, sp) => n + sp.amount, 0) !== t.amount
    )
      throw new Error("Splits must add up to the full amount.");
    t.splits = a.splits;
    t.category = null;
    t.status = "confirmed";
    t.ignored = false;
  }
  if (a.type === "note" && t) t.note = a.note;
  if (a.type === "add-savings") {
    if (!a.amount || a.amount <= 0 || !a.fromAccountId)
      throw new Error("Choose an amount and an account.");
    s.transactions.push({
      id: a.id,
      accountId: a.fromAccountId,
      date: a.date || new Date().toISOString().slice(0, 10),
      amount: a.amount,
      merchant: a.note || "added to savings",
      category: "savings",
      status: "confirmed",
      direction: "out",
    });
  }
  if (a.type === "account-settings" && a.accountId) {
    const account = s.accounts.find((acc) => acc.id === a.accountId);
    if (account) {
      if (a.excluded !== undefined) account.excludedFromSafeToSpend = a.excluded;
      if (a.clearReauth) account.needsReauth = false;
    }
  }
  s.goal.saved += savedTotal(s) - previousSaved;
  return s;
}
export function demoState(): State {
  const now = new Date();
  const date = (day: number, offset = 0) =>
    `${new Date(now.getFullYear(), now.getMonth() + offset, 1).getFullYear()}-${String(new Date(now.getFullYear(), now.getMonth() + offset, 1).getMonth() + 1).padStart(2, "0")}-${String(Math.min(day, 28)).padStart(2, "0")}`;
  const tx: Transaction[] = [];
  for (let m = -3; m < 0; m++) {
    tx.push({
      id: `salary${m}`,
      accountId: "checking",
      date: date(15, m),
      amount: 480000,
      merchant: "Acme Studio payroll",
      category: "expenses",
      status: "confirmed",
      direction: "in",
      incomeStream: "salary",
    });
    tx.push({
      id: `freelance${m}`,
      accountId: "checking",
      date: date(20, m),
      amount: { [-3]: 95000, [-2]: 165000, [-1]: 110000 }[m] || 0,
      merchant: "Freelance design",
      category: "spending",
      status: "confirmed",
      direction: "in",
      incomeStream: "self-employed",
    });
  }
  const merchants: [string, number, Category, string][] = [
    ["Whole Foods Market", 8642, "spending", "W"],
    ["Blue Bottle Coffee", 650, "spending", "B"],
    ["Spotify", 1199, "expenses", "S"],
    ["Trader Joe’s", 5247, "spending", "T"],
    ["Rent · Oakwood", 165000, "expenses", "O"],
    ["Emergency fund", 60000, "savings", "E"],
    ["Dinner at Lilia", 12400, "spending", "L"],
    ["Internet · AT&T", 7500, "expenses", "A"],
  ];
  merchants.forEach(([merchant, amount, category], i) =>
    tx.push({
      id: `t${i}`,
      accountId: "checking",
      date: date(Math.max(1, Math.min(now.getDate(), 23) - Math.floor(i / 2))),
      amount,
      merchant,
      category,
      subcategoryId: merchant.includes("Rent") ? "rent" : merchant.includes("Internet") ? "utilities" : merchant.includes("Spotify") ? "subscriptions" : merchant.includes("Whole Foods") || merchant.includes("Trader Joe") ? "groceries" : merchant.includes("Coffee") || merchant.includes("Dinner") ? "food-out" : merchant.includes("fund") ? "future" : "other",
      status: "confirmed",
      direction: "out",
    }),
  );
  [
    ["NETFLIX.COM", 1599, "expenses"],
    ["Sunday Bookshop", 2800, "spending"],
    ["Venmo · Jamie", 6200, "spending"],
    ["Figma", 1500, "expenses"],
    ["Sweetgreen", 1675, "spending"],
  ].forEach(([merchant, amount, category], i) =>
    tx.push({
      id: `pending${i}`,
      accountId: "checking",
      date: date(Math.min(now.getDate(), 23)),
      amount: Number(amount),
      merchant: String(merchant),
      category: null,
      suggested: category as Category,
      suggestedSubcategoryId: String(merchant).includes("NETFLIX") || String(merchant).includes("Figma") ? "subscriptions" : String(merchant).includes("Sweetgreen") ? "food-out" : undefined,
      status: "pending",
      direction: i === 2 ? "in" : "out",
    }),
  );
  return {
    demo: true,
    lastSync: now.toISOString(),
    transactions: tx,
    categories: {
      expenses: "expenses",
      spending: "spending",
      savings: "savings",
    },
    threshold: 3,
    rules: [],
    ignoreRules: [],
    subcategories: [
      { id: "rent", name: "rent", group: "expenses", monthlyPlan: 165000 },
      { id: "utilities", name: "utilities", group: "expenses", monthlyPlan: 25000 },
      { id: "subscriptions", name: "subscriptions", group: "expenses", monthlyPlan: 5000 },
      { id: "groceries", name: "groceries", group: "spending", monthlyPlan: 50000 },
      { id: "food-out", name: "food out", group: "spending", monthlyPlan: 20000 },
      { id: "other", name: "other", group: "spending", monthlyPlan: 30000 },
      { id: "future", name: "future", group: "savings", monthlyPlan: 60000 },
    ],
    links: [],
    goal: {
      name: "a little breathing room",
      target: 1000000,
      monthly: 60000,
      saved: 680000,
    },
    accounts: [
      {
        id: "checking",
        name: "Everyday Checking",
        institution: "Chase",
        type: "depository",
        subtype: "checking",
        mask: "4821",
        balance: 824650,
        syncedAt: now.toISOString(),
      },
      {
        id: "savings",
        name: "High-Yield Savings",
        institution: "Ally",
        type: "depository",
        subtype: "savings",
        mask: "9032",
        balance: 680000,
        syncedAt: now.toISOString(),
      },
      {
        id: "investment",
        name: "Individual Brokerage",
        institution: "Fidelity",
        type: "investment",
        subtype: "brokerage",
        mask: "1106",
        balance: 2432850,
        syncedAt: now.toISOString(),
        needsReauth: true,
      },
    ],
    recurring: [
      {
        id: "demo-candidate",
        merchant: "Cloud storage",
        amount: 299,
        tolerance: 10,
        cadence: "monthly",
        type: "bill",
        category: "expenses",
        confirmed: false,
        threshold: 3,
        nextDate: date(4, 1),
      },
      {
        id: "r1",
        merchant: "Rent · Oakwood",
        amount: 165000,
        tolerance: 0,
        cadence: "monthly",
        type: "bill",
        category: "expenses",
        confirmed: true,
        threshold: 3,
        nextDate: date(1, 1),
      },
      {
        id: "r2",
        merchant: "Internet · AT&T",
        amount: 7500,
        tolerance: 5,
        cadence: "monthly",
        type: "bill",
        category: "expenses",
        confirmed: true,
        threshold: 3,
        nextDate: date(25),
      },
      {
        id: "r3",
        merchant: "Spotify",
        amount: 1199,
        tolerance: 0,
        cadence: "monthly",
        type: "bill",
        category: "expenses",
        confirmed: true,
        threshold: 3,
        nextDate: date(27),
      },
      {
        id: "r4",
        merchant: "Electricity",
        amount: 14500,
        tolerance: 20,
        cadence: "monthly",
        type: "bill",
        category: "expenses",
        confirmed: true,
        threshold: 3,
        nextDate: date(28),
      },
      {
        id: "r5",
        merchant: "Car insurance",
        amount: 18301,
        tolerance: 5,
        cadence: "monthly",
        type: "bill",
        category: "expenses",
        confirmed: true,
        threshold: 3,
        nextDate: date(3, 1),
      },
    ],
  };
}
