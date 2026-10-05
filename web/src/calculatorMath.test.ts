import { describe, expect, it } from "vitest";
import { debtPayoff, emergencyRunway, investmentGrowth, monthsToSave } from "./calculatorMath";

describe("financial calculators", () => {
  it("estimates savings time, including a completed or stalled goal", () => {
    expect(monthsToSave(100_000, 20_000, 10_000)).toBe(8);
    expect(monthsToSave(100_000, 100_000, 0)).toBe(0);
    expect(monthsToSave(100_000, 20_000, 0)).toBeNull();
  });

  it("calculates runway and cash gaps", () => {
    expect(emergencyRunway(5_000, 2_000)).toEqual({ months: 2.5, threeMonthGap: 1_000, sixMonthGap: 7_000 });
    expect(emergencyRunway(5_000, 0)).toBeNull();
  });

  it("amortizes debt and identifies payments that cannot reduce principal", () => {
    expect(debtPayoff(10_000, 0, 2_500)).toEqual({ months: 4, interest: 0 });
    expect(debtPayoff(10_000, 12, 1_000)).toEqual({ months: 11, interest: 590 });
    expect(debtPayoff(10_000, 12, 100)).toBeNull();
  });

  it("compounds contributions at month end and handles losses", () => {
    expect(investmentGrowth(10_000, 1_000, 0, 1)).toEqual({ value: 22_000, contributed: 22_000, growth: 0 });
    expect(investmentGrowth(10_000, 0, -12, 1).growth).toBeLessThan(0);
  });
});
