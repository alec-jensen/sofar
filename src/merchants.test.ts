import { describe, expect, it } from "vitest";
import { defaultCategories, lookupMerchant } from "./merchants";

// Mirrors internal/budget/merchants_test.go so the app and server sort alike.
describe("merchant directory", () => {
  it("sorts common merchants the same way the server does", () => {
    const cases: Record<string, string> = {
      "McDonald's": "dining", Whataburger: "dining", "Handel's Ice Cream": "coffee", Shell: "gas",
      QuikTrip: "gas", AutoZone: "transport", Amazon: "shopping", "Google Fi": "phone",
      "Ajs Hot Chicken": "dining", "Martin Hs Theatre": "entertainment", "Kwik Market": "gas",
      "Fidelity Brokerage Services": "investing", "Round Rock Donuts": "dining", "Uber Eats": "dining", Uber: "transport",
    };
    for (const [name, sub] of Object.entries(cases)) expect(lookupMerchant(name)?.sub, name).toBe(sub);
  });
  it("leaves unknown names alone", () => expect(lookupMerchant("Point of Rental")).toBeNull());
  it("offers categories in all three groups", () => {
    for (const group of ["expenses", "spending", "savings"]) expect(defaultCategories.some((c) => c.group === group)).toBe(true);
  });
});
