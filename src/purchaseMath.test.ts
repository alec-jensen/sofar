import { describe, expect, it } from "vitest";
import { investmentProjection, purchaseImpact } from "./purchaseMath";

describe("purchase impact", () => {
  it("compares a price to the full month and confirmed spending", () => {
    const impact = purchaseImpact(15_000, 90_000, 20_000, 30);
    expect(impact.shareOfAllowance).toBeCloseTo(100 / 6);
    expect(impact).toMatchObject({
      daysOfAllowance: 5,
      roomBefore: 70_000,
      roomAfter: 55_000,
    });
  });

  it("does not claim a share or days when the plan is zero or negative", () => {
    expect(purchaseImpact(5_000, -1_000, 2_000, 31)).toEqual({
      shareOfAllowance: null,
      daysOfAllowance: null,
      roomBefore: -3_000,
      roomAfter: -8_000,
    });
  });
});

describe("investment projection", () => {
  it("compounds a single amount and reports only hypothetical growth", () => {
    expect(investmentProjection(10_000, 5, 10)).toEqual({
      projected: 16_289,
      growth: 6_289,
    });
  });

  it("shows a possible loss under a negative assumption", () => {
    expect(investmentProjection(10_000, -10, 1)).toEqual({
      projected: 9_000,
      growth: -1_000,
    });
  });
});
