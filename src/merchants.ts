// sofar's built-in merchant directory, shared with the Go server so both sort
// the same way. See internal/budget/merchants.go for the server side.
import directory from "../internal/budget/merchants.json";
import { normalize, type Category } from "./model";

type Entry = { sub: string; name?: string; match: string[] };

export const defaultCategories = directory.categories as { id: string; name: string; group: Category }[];
export const knownMerchantCount = directory.merchants.length;

const hasWords = (normalized: string, pattern: string) => ` ${normalized} `.includes(` ${pattern} `);

function longest(normalized: string, entries: Entry[]) {
  let best: Entry | null = null,
    size = 0;
  for (const entry of entries)
    for (const pattern of entry.match)
      if (pattern.length > size && hasWords(normalized, pattern)) {
        best = entry;
        size = pattern.length;
      }
  return best;
}

/** The default category for spending at a merchant, if sofar recognizes it. */
export function lookupMerchant(name: string): { sub: string; group: Category; name?: string } | null {
  const n = normalize(name);
  const found = longest(n, directory.merchants) || longest(n, directory.keywords);
  if (!found) return null;
  const group = defaultCategories.find((c) => c.id === found.sub)?.group;
  return group ? { sub: found.sub, group, name: found.name } : null;
}
