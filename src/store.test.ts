import "fake-indexeddb/auto";
import { afterEach, expect, it, vi } from "vitest";
import { openDB } from "idb";
import { demoState, type Action } from "./model";
import { clearPrivate, dispatch, flush, loadDemo, save } from "./store";

afterEach(async () => {
  await clearPrivate();
  vi.unstubAllGlobals();
});

it("replays dependent offline changes in insertion order, not random UUID order", async () => {
  vi.stubGlobal("navigator", { onLine: false });
  const ids = [
    "zzzzzzzz-0000-4000-8000-000000000001",
    "aaaaaaaa-0000-4000-8000-000000000002",
  ];
  vi.stubGlobal("crypto", { randomUUID: () => ids.shift() });
  let state = { ...demoState(), demo: false };
  state = await dispatch(state, {
    type: "review",
    transactionId: "pending0",
    category: "expenses",
  });
  state = await dispatch(state, {
    type: "review",
    transactionId: "pending0",
    category: "savings",
  });
  const sent: Action[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (_url: string, options?: RequestInit) => {
      if (options?.body) sent.push(JSON.parse(String(options.body)));
      return new Response(JSON.stringify(state), { status: 200 });
    }),
  );
  await flush();
  expect(sent.map((a) => a.category)).toEqual(["expenses", "savings"]);
  expect(sent[0]).not.toHaveProperty("sequence");
  expect(sent[0].id).toMatch(/^z/);
  const db = await openDB("sofar-v1", 1);
  expect(await db.count("queue")).toBe(0);
  db.close();
});

it("retains a rejected queued action for retry rather than silently dropping it", async () => {
  vi.stubGlobal("navigator", { onLine: false });
  await dispatch(
    { ...demoState(), demo: false },
    { type: "review", transactionId: "pending0", category: "expenses" },
  );
  vi.stubGlobal(
    "fetch",
    vi.fn(
      async () =>
        new Response(JSON.stringify({ error: "Please sign in to continue." }), {
          status: 401,
        }),
    ),
  );
  await expect(flush()).rejects.toThrow("Please sign in");
  const db = await openDB("sofar-v1", 1);
  expect(await db.count("queue")).toBe(1);
  db.close();
});

it("adds the new recurring review example to an existing demo without clearing edits", async () => {
  const old = demoState();
  old.goal.name = "my edited goal";
  old.categories.expenses = "Expenses";
  old.recurring = old.recurring.filter((r) => r.id !== "demo-candidate");
  await save(old);
  const loaded = await loadDemo();
  expect(loaded.goal.name).toBe("my edited goal");
  expect(loaded.categories.expenses).toBe("expenses");
  expect(loaded.recurring.some((r) => r.id === "demo-candidate")).toBe(true);
  const db = await openDB("sofar-v1", 1);
  await db.delete("cache", "demo");
  db.close();
});
