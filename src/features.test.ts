import { describe, expect, it } from "vitest";
import { applyAction, demoState, nextDue, reimbursementMatches, type Transaction } from "./model";

const pending = (id: string, merchant: string): Transaction => ({
  id, accountId: "checking", date: "2026-09-10", amount: 1000, merchant,
  category: null, status: "pending", direction: "out",
});

describe("review shortcuts", () => {
  it("sorts every waiting transaction from the same merchant when asked", () => {
    const s = demoState();
    s.transactions.push(pending("u1", "Uber 063015"), pending("u2", "UBER 072515"), pending("x", "Lyft"));
    const next = applyAction(s, { id: "a1", type: "review", transactionId: "u1", category: "spending", always: true });
    expect(next.transactions.find((t) => t.id === "u2")).toMatchObject({ status: "confirmed", category: "spending" });
    expect(next.transactions.find((t) => t.id === "x")?.status).toBe("pending");
  });
  it("leaves other matches alone without the shortcut", () => {
    const s = demoState();
    s.transactions.push(pending("u1", "Uber"), pending("u2", "Uber"));
    const next = applyAction(s, { id: "a1", type: "review", transactionId: "u1", category: "spending" });
    expect(next.transactions.find((t) => t.id === "u2")?.status).toBe("pending");
  });
  it("always-ignore also ignores matches waiting for review", () => {
    const s = demoState();
    s.transactions.push(pending("c1", "Card payment"), pending("c2", "Card payment"));
    const next = applyAction(s, { id: "a1", type: "ignore", transactionId: "c1", reason: "card payment", always: true });
    expect(next.transactions.find((t) => t.id === "c2")).toMatchObject({ ignored: true, status: "confirmed" });
  });
});

describe("manual entries", () => {
  it("deletes deposits you added and removes them from savings", () => {
    const s = demoState();
    const added = applyAction(s, { id: "dep", type: "add-savings", amount: 2500, fromAccountId: "checking", note: "cash jar" });
    expect(added.goal.saved).toBe(s.goal.saved + 2500);
    const removed = applyAction(added, { id: "del", type: "delete-transaction", transactionId: "dep" });
    expect(removed.transactions.some((t) => t.id === "dep")).toBe(false);
    expect(removed.goal.saved).toBe(s.goal.saved);
  });
  it("refuses to delete bank transactions", () => {
    expect(() => applyAction(demoState(), { id: "del", type: "delete-transaction", transactionId: "t0" })).toThrow();
  });
  it("adds a confirmed bill that counts as a commitment", () => {
    const next = applyAction(demoState(), { id: "b", type: "recurring-create", merchant: "Phone", amount: 4500, cadence: "monthly", nextDate: "2026-10-07", category: "expenses" });
    expect(next.recurring.find((r) => r.merchant === "Phone")).toMatchObject({ confirmed: true, type: "bill", amount: 4500 });
  });
  it("renames an account", () => {
    const next = applyAction(demoState(), { id: "n", type: "account-settings", accountId: "checking", name: "bills" });
    expect(next.accounts.find((a) => a.id === "checking")?.name).toBe("bills");
  });
});

describe("next due date", () => {
  const today = new Date(2026, 8, 30);
  it("keeps future dates", () => expect(nextDue({ nextDate: "2026-10-07", cadence: "monthly" }, today)).toBe("2026-10-07"));
  it("rolls monthly bills forward, clamping to month length", () => expect(nextDue({ nextDate: "2026-01-31", cadence: "monthly" }, today)).toBe("2026-09-30"));
  it("rolls weekly bills forward", () => expect(nextDue({ nextDate: "2026-09-01", cadence: "weekly" }, today)).toBe("2026-10-06"));
});

describe("auto-categorize respects your rules", () => {
  it("leaves a merchant alone when your rule says just the group", () => {
    const s = demoState();
    s.rules.push({ pattern: "sonic", category: "spending", enabled: true });
    s.transactions.push({ id: "so", accountId: "checking", date: "2026-09-10", amount: 750, merchant: "Sonic", category: "spending", status: "confirmed", direction: "out", suggestedSubcategoryId: "dining", suggested: "spending" });
    const next = applyAction(s, { id: "ac", type: "auto-categorize" });
    expect(next.transactions.find((t) => t.id === "so")?.subcategoryId).toBeUndefined();
  });
  it("still sorts merchants without a rule", () => {
    const s = demoState();
    s.transactions.push({ id: "tt", accountId: "checking", date: "2026-09-10", amount: 1500, merchant: "Torchy's Tacos", category: "spending", status: "confirmed", direction: "out", suggestedSubcategoryId: "dining", suggested: "spending" });
    const next = applyAction(s, { id: "ac", type: "auto-categorize" });
    expect(next.transactions.find((t) => t.id === "tt")?.subcategoryId).toBe("dining");
  });
});

describe("sorting rules", () => {
  it("are only learned from spending, not from who pays you", () => {
    const s = demoState();
    const deposit = s.transactions.find((t) => t.direction === "in" && t.status === "pending")!;
    const next = applyAction(s, { id: "r1", type: "review", transactionId: deposit.id, category: "spending", incomeStream: "salary" });
    expect(next.rules).toHaveLength(0);
    const purchase = s.transactions.find((t) => t.direction === "out" && t.status === "pending")!;
    expect(applyAction(s, { id: "r2", type: "review", transactionId: purchase.id, category: "spending" }).rules).toHaveLength(1);
  });
});

describe("repayment candidates", () => {
  it("skip ignored and split expenses", () => {
    const s = demoState();
    const credit = { ...s.transactions.find((t) => t.direction === "in" && t.status === "pending")! };
    credit.date = "2099-01-01";
    const spend = s.transactions.filter((t) => t.direction === "out" && t.status === "confirmed");
    spend[0].ignored = true;
    spend[1].splits = [{ category: "spending", amount: spend[1].amount - 1 }, { category: "savings", amount: 1 }];
    const ids = reimbursementMatches(s, credit).map((t) => t.id);
    expect(ids).not.toContain(spend[0].id);
    expect(ids).not.toContain(spend[1].id);
    expect(ids).toContain(spend[2].id);
  });
});
