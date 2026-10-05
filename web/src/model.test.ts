import { describe, it, expect } from "vitest";
import {
  applyAction,
  budget,
  dailyAllowance,
  demoState,
  reimbursementMatches,
  normalize,
} from "./model";
describe("money rules", () => {
  it("spreads the monthly allowance across the selected month's days", () => {
    expect(dailyAllowance(336833, new Date(2026, 8, 1))).toBe(11228);
    expect(dailyAllowance(336833, new Date(2028, 1, 1))).toBe(11615);
    expect(dailyAllowance(-3000, new Date(2026, 8, 1))).toBe(-100);
  });
  it("keeps income streams separate, dividing each by three months", () => {
    const s = demoState();
    const b = budget(s);
    expect(b.salary).toBe(480000);
    expect(b.selfEmployed).toBe(123333);
    expect(b.bills).toBe(206500);
    expect(b.safe).toBe(336833);
  });
  it("excludes unconfirmed income and repayments from baseline", () => {
    const s = demoState();
    s.transactions.push({
      ...s.transactions[0],
      id: "pending-income",
      status: "pending",
      amount: 99999999,
    });
    s.links.push({
      id: "repayment",
      expenseId: "t0",
      creditId: s.transactions[0].id,
      amount: 100,
    });
    expect(budget(s).salary).toBe(320000);
  });
  it("finds an expense in another account and nets a partial repayment", () => {
    const s = demoState();
    const credit = s.transactions.find((t) => t.id === "pending2")!;
    credit.accountId = "savings";
    const matches = reimbursementMatches(s, credit);
    expect(matches.some((t) => t.id === "t6")).toBe(true);
    const next = applyAction(s, {
      id: "partial",
      type: "reimburse",
      creditId: credit.id,
      expenseId: "t6",
      amount: 6200,
    });
    expect(next.links[0].amount).toBe(6200);
    expect(budget(next).totals.spending).toBe(budget(s).totals.spending - 6200);
    expect(
      next.transactions.find((t) => t.id === credit.id)?.incomeStream,
    ).toBeUndefined();
  });
  it("prevents over-reimbursement and duplicate credits", () => {
    const s = demoState();
    expect(() =>
      applyAction(s, {
        id: "bad",
        type: "reimburse",
        creditId: "pending2",
        expenseId: "t6",
        amount: 6201,
      }),
    ).toThrow();
    const next = applyAction(s, {
      id: "ok",
      type: "reimburse",
      creditId: "pending2",
      expenseId: "t6",
      amount: 6200,
    });
    expect(() =>
      applyAction(next, {
        id: "duplicate",
        type: "reimburse",
        creditId: "pending2",
        expenseId: "t6",
        amount: 1,
      }),
    ).toThrow();
  });
  it("normalizes variations when learning categories", () => {
    expect(normalize("NETFLIX.COM")).toBe(normalize("Netflix 08/23"));
    const s = applyAction(demoState(), {
      id: "review",
      type: "review",
      transactionId: "pending0",
      category: "expenses",
    });
    expect(s.rules.find((r) => r.pattern === "netflix")?.category).toBe(
      "expenses",
    );
    expect(s.transactions.find((t) => t.id === "pending0")?.status).toBe(
      "confirmed",
    );
  });
  it("counts only confirmed recurring expenses and never clamps a negative allowance", () => {
    const s = demoState();
    s.goal.monthly = 9999999;
    s.recurring.push({
      ...s.recurring[0],
      id: "unconfirmed",
      amount: 9999999,
      confirmed: false,
    });
    expect(budget(s).bills).toBe(206500);
    expect(budget(s).safe).toBeLessThan(0);
  });
  it("lets a false recurring pattern be dismissed and restored", () => {
    const s = demoState();
    const candidate = { ...s.recurring[0], id: "candidate", confirmed: false };
    s.recurring.push(candidate);
    const dismissed = applyAction(s, {
      id: "dismiss",
      type: "dismiss-recurring",
      recurringId: candidate.id,
    });
    expect(dismissed.recurring.at(-1)?.dismissed).toBe(true);
    const restored = applyAction(dismissed, {
      id: "restore",
      type: "restore-recurring",
      recurringId: candidate.id,
    });
    expect(restored.recurring.at(-1)?.dismissed).toBe(false);
  });
  it("reserves expense plans above detected bills and keeps category moves consistent", () => {
    const s = demoState();
    const prior = budget(s).safe;
    const rent = s.subcategories!.find(c => c.id === "housing")!;
    const planned = applyAction(s, { id: "plan", type: "subcategory-upsert", subcategory: { ...rent, monthlyPlan: 300000 } });
    expect(budget(planned).safe).toBeLessThan(prior);
    const moved = applyAction(planned, { id: "move", type: "subcategory-upsert", subcategory: { ...rent, group: "spending" } });
    expect(moved.transactions.find(t => t.id === "t4")?.category).toBe("spending");
    const deleted = applyAction(moved, { id: "delete", type: "subcategory-delete", subcategoryId: rent.id });
    expect(deleted.transactions.find(t => t.id === "t4")?.subcategoryId).toBeUndefined();
  });
  it("ignores a transaction and, when marked always, remembers the merchant", () => {
    const s = demoState();
    const before = budget(s).totals.expenses;
    const next = applyAction(s, {
      id: "ignore1",
      type: "ignore",
      transactionId: "pending0",
      reason: "not mine",
      always: true,
    });
    const t = next.transactions.find((t) => t.id === "pending0")!;
    expect(t.status).toBe("confirmed");
    expect(t.ignored).toBe(true);
    expect(next.ignoreRules.some((r) => r.pattern === "netflix")).toBe(true);
    expect(budget(next).totals.expenses).toBe(before);
  });
  it("splits a transaction across categories and totals each part", () => {
    const s = demoState();
    const t0 = s.transactions.find((t) => t.id === "t0")!;
    const next = applyAction(s, {
      id: "split1",
      type: "split",
      transactionId: "t0",
      splits: [
        { category: "spending", amount: Math.round(t0.amount / 2) },
        { category: "savings", amount: t0.amount - Math.round(t0.amount / 2) },
      ],
    });
    const t = next.transactions.find((t) => t.id === "t0")!;
    expect(t.splits).toHaveLength(2);
    const b = budget(next);
    expect(b.totals.spending + b.totals.savings).toBeGreaterThanOrEqual(
      Math.round(t0.amount / 2),
    );
    expect(() =>
      applyAction(s, {
        id: "bad-split",
        type: "split",
        transactionId: "t0",
        splits: [{ category: "spending", amount: 1 }],
      }),
    ).toThrow();
    expect(() =>
      applyAction(s, {
        id: "split-income",
        type: "split",
        transactionId: "pending2",
        splits: [
          { category: "spending", amount: 3100 },
          { category: "savings", amount: 3100 },
        ],
      }),
    ).toThrow();
  });
  it("adds a manual savings deposit and updates saved total", () => {
    const s = demoState();
    const before = s.goal.saved;
    const next = applyAction(s, {
      id: "deposit1",
      type: "add-savings",
      amount: 5000,
      fromAccountId: "checking",
    });
    expect(next.goal.saved).toBe(before + 5000);
  });
  it("excludes an account from safe-to-spend and clears reauth", () => {
    const s = demoState();
    const withNeed = { ...s, accounts: s.accounts.map((a) => a.id === "checking" ? { ...a, needsReauth: true } : a) };
    const before = budget(withNeed).salary;
    const excluded = applyAction(withNeed, {
      id: "exclude1",
      type: "account-settings",
      accountId: "checking",
      excluded: true,
    });
    expect(budget(excluded).salary).toBe(0);
    expect(budget(excluded).salary).not.toBe(before);
    const reauthed = applyAction(excluded, {
      id: "reauth1",
      type: "account-settings",
      accountId: "checking",
      clearReauth: true,
    });
    expect(reauthed.accounts.find((a) => a.id === "checking")?.needsReauth).toBe(false);
  });
  it("backfills a rule's category onto past confirmed matches", () => {
    const s = demoState();
    const next = applyAction(s, {
      id: "backfill1",
      type: "rule-backfill",
      pattern: "whole foods",
      category: "expenses",
      subcategoryId: "housing",
    });
    const t = next.transactions.find((t) => t.id === "t0")!;
    expect(t.category).toBe("expenses");
    expect(t.subcategoryId).toBe("housing");
  });
  it("can pause, edit, and delete sorting rules", () => {
    const s = demoState();
    const added = applyAction(s, { id: "rule1", type: "rule-upsert", pattern: "trader joe", category: "spending", subcategoryId: "groceries" });
    expect(added.rules[0].subcategoryId).toBe("groceries");
    const paused = applyAction(added, { id: "rule2", type: "rule-toggle", pattern: "trader joe", enabled: false });
    expect(paused.rules[0].enabled).toBe(false);
    const renamed = applyAction(paused, { id: "rule3", type: "rule-upsert", oldPattern: "trader joe", pattern: "trader", category: "expenses" });
    expect(renamed.rules.map(r => r.pattern)).toEqual(["trader"]);
    const removed = applyAction(renamed, { id: "rule4", type: "rule-delete", pattern: "trader" });
    expect(removed.rules).toHaveLength(0);
  });
});
