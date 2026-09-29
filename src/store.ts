import { openDB } from "idb";
import { applyAction, demoState, type Action, type State } from "./model";
const db = openDB("sofar-v1", 1, {
  upgrade(db) {
    db.createObjectStore("cache");
    db.createObjectStore("queue", { keyPath: "id" });
  },
});
export async function api(path: string, body?: unknown) {
  const r = await fetch("/api" + path, {
    method: body ? "POST" : "GET",
    credentials: "same-origin",
    headers: body ? { "Content-Type": "application/json" } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!r.ok) {
    const data = await r
      .json()
      .catch(() => ({ error: "Could not reach sofar." }));
    throw new Error(data.error || "Something went wrong.");
  }
  return r.json();
}
export async function loadDemo() {
  const saved = (await (await db).get("cache", "demo")) as State | undefined;
  if (!saved) return demoState();
  for (const category of ["expenses", "spending", "savings"] as const) {
    const oldDefault = category[0].toUpperCase() + category.slice(1);
    if (saved.categories[category] === oldDefault)
      saved.categories[category] = category;
  }
  if (saved.goal.name === "A little breathing room")
    saved.goal.name = "a little breathing room";
  if (!saved.subcategories) {
    const fresh = demoState();
    saved.subcategories = fresh.subcategories;
    for (const transaction of saved.transactions) {
      const example = fresh.transactions.find(t => t.id === transaction.id);
      if (example) {
        transaction.subcategoryId = example.subcategoryId;
        transaction.suggestedSubcategoryId = example.suggestedSubcategoryId;
      }
    }
  }
  if (!saved.recurring.some((r) => r.id === "demo-candidate")) {
    saved.recurring.push(demoState().recurring[0]);
  }
  if (!saved.ignoreRules) saved.ignoreRules = [];
  await save(saved);
  return saved;
}
export async function save(s: State) {
  await (await db).put("cache", s, s.demo ? "demo" : "live");
}
export async function cached() {
  return (await db).get("cache", "live") as Promise<State | undefined>;
}
export async function clearPrivate() {
  const d = await db;
  await d.delete("cache", "live");
  await d.clear("queue");
}
async function queueAction(action: Action) {
  const transaction = (await db).transaction(["cache", "queue"], "readwrite");
  const sequence =
    ((await transaction.objectStore("cache").get("actionSequence")) || 0) + 1;
  await transaction.objectStore("cache").put(sequence, "actionSequence");
  await transaction.objectStore("queue").put({ ...action, sequence });
  await transaction.done;
  if ("serviceWorker" in navigator) {
    void navigator.serviceWorker.ready
      .then((reg) =>
        (
          reg as ServiceWorkerRegistration & {
            sync?: { register: (tag: string) => Promise<void> };
          }
        ).sync?.register("sofar-actions"),
      )
      .catch(() => {
        /* The online event also replays queued changes. */
      });
  }
}
export async function dispatch(s: State, a: Omit<Action, "id">) {
  const action = { ...a, id: crypto.randomUUID() };
  let next = applyAction(s, action);
  if (!s.demo) {
    if (navigator.onLine) {
      try {
        if (await (await db).count("queue")) await flush();
        next = await api("/actions", action);
      } catch (error) {
        if (error instanceof TypeError) {
          await queueAction(action);
        } else throw error;
      }
    } else {
      await queueAction(action);
    }
  }
  await save(next);
  return next;
}
export async function flush() {
  const d = await db;
  const pending = (await d.getAll("queue")).sort(
    (a, b) => (a.sequence || 0) - (b.sequence || 0),
  );
  for (const queued of pending) {
    const { sequence: _sequence, ...action } = queued;
    await api("/actions", action);
    await d.delete("queue", action.id);
  }
  const s: State = await api("/state");
  await save(s);
  return s;
}
