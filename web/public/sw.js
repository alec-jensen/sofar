const CACHE = "sofar-shell-v1";
const PRECACHE = [];
self.addEventListener("install", (event) => {
  event.waitUntil(
    caches
      .open(CACHE)
      .then((cache) =>
        cache.addAll([
          "/",
          "/icon.svg",
          "/icon-192.png",
          "/icon-512.png",
          "/manifest.webmanifest",
          ...PRECACHE,
        ]),
      ),
  );
  self.skipWaiting();
});
self.addEventListener("activate", (event) => {
  event.waitUntil(
    caches
      .keys()
      .then((keys) =>
        Promise.all(
          keys
            .filter((k) => k.startsWith("sofar-shell-") && k !== CACHE)
            .map((k) => caches.delete(k)),
        ),
      )
      .then(() => self.clients.claim()),
  );
});
self.addEventListener("fetch", (event) => {
  const url = new URL(event.request.url);
  if (
    event.request.method !== "GET" ||
    url.origin !== self.location.origin ||
    url.pathname.startsWith("/api/") ||
    url.pathname === "/sw.js"
  )
    return;
  event.respondWith(
    fetch(event.request)
      .then((response) => {
        if (response.ok && response.type === "basic") {
          const copy = response.clone();
          caches.open(CACHE).then((cache) => cache.put(event.request, copy));
        }
        return response;
      })
      .catch(async () => {
        const cached = await caches.match(event.request);
        if (cached) return cached;
        if (event.request.mode === "navigate") return await caches.match("/");
        return Response.error();
      }),
  );
});
self.addEventListener("push", (event) => {
  let data = {
    title: "sofar",
    body: "A few things to review.",
    count: 0,
    url: "/#review",
    tag: "sofar-review",
  };
  try {
    Object.assign(data, event.data.json());
  } catch {}
  event.waitUntil(
    Promise.all([
      self.registration.showNotification(data.title, {
        body: data.body,
        icon: "/icon-192.png",
        badge: "/icon-192.png",
        tag: "sofar-review",
        data: { url: "/#review" },
      }),
      self.navigator.setAppBadge
        ? self.navigator.setAppBadge(data.count)
        : Promise.resolve(),
    ]),
  );
});
self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  event.waitUntil(
    self.clients
      .matchAll({ type: "window", includeUncontrolled: true })
      .then((clients) => {
        for (const client of clients) {
          if (new URL(client.url).origin === self.location.origin) {
            client.postMessage({ type: "open-review" });
            return client.focus();
          }
        }
        return self.clients.openWindow("/#review");
      }),
  );
});
self.addEventListener("sync", (event) => {
  if (event.tag === "sofar-actions") event.waitUntil(flushActions());
});
async function flushActions() {
  const db = await new Promise((resolve, reject) => {
    const request = indexedDB.open("sofar-v1", 1);
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
  });
  const requestResult = (request) =>
    new Promise((resolve, reject) => {
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
  try {
    if (!db.objectStoreNames.contains("queue")) return;
    const actions = await requestResult(
      db.transaction("queue").objectStore("queue").getAll(),
    );
    actions.sort((a, b) => (a.sequence || 0) - (b.sequence || 0));
    for (const queued of actions) {
      const { sequence: _sequence, ...action } = queued;
      const response = await fetch("/api/actions", {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(action),
      });
      if (!response.ok)
        throw new Error("Queued action needs an authenticated retry.");
      await requestResult(
        db
          .transaction("queue", "readwrite")
          .objectStore("queue")
          .delete(action.id),
      );
    }
    if (actions.length) {
      const response = await fetch("/api/state", {
        credentials: "same-origin",
        cache: "no-store",
      });
      if (response.ok) {
        const state = await response.json();
        await requestResult(
          db
            .transaction("cache", "readwrite")
            .objectStore("cache")
            .put(state, "live"),
        );
      }
    }
    const clients = await self.clients.matchAll({ type: "window" });
    clients.forEach((client) => client.postMessage({ type: "flush-actions" }));
  } finally {
    db.close();
  }
}
