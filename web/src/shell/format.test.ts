import { describe, expect, it } from "vitest";
import { dollars, dollarsAndCents } from "./format";

describe("format", () => {
  it("rounds to dollars and keeps the sign", () => {
    expect(dollars(123456)).toBe("$1,235");
    expect(dollars(-4999)).toBe("-$50");
    expect(dollars(0)).toBe("$0");
  });
  it("shows cents when asked", () => {
    expect(dollarsAndCents(123456)).toBe("$1,234.56");
    expect(dollarsAndCents(-5)).toBe("-$0.05");
  });
});
