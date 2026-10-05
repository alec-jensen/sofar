import { describe, expect, it } from "vitest";
import { decideRelease, releaseVelocity } from "./useSwipeCard";

describe("swipe release", () => {
  it("commits a slow, deliberate drag by distance", () => {
    expect(decideRelease(100, 0)).toBe("confirm");
    expect(decideRelease(-100, 0)).toBe("change");
    expect(decideRelease(60, 0)).toBe("snap");
  });
  it("commits a short, fast flick by its momentum", () => {
    expect(decideRelease(50, 0.8)).toBe("confirm");
    expect(decideRelease(-50, -0.8)).toBe("change");
    expect(decideRelease(70, 0.5)).toBe("confirm");
  });
  it("ignores a twitch and a flick in the wrong direction", () => {
    expect(decideRelease(15, 2)).toBe("snap");
    expect(decideRelease(60, -0.8)).toBe("snap");
    expect(decideRelease(40, 0.2)).toBe("snap");
  });
  it("won't confirm when confirming isn't allowed", () => {
    expect(decideRelease(120, 0, false)).toBe("snap");
    expect(decideRelease(50, 0.9, false)).toBe("snap");
    expect(decideRelease(-120, 0, false)).toBe("change");
  });
});

describe("release velocity", () => {
  const trail = [{ x: 100, t: 1000 }, { x: 130, t: 1030 }, { x: 160, t: 1060 }];
  it("measures the last moments of movement", () => expect(releaseVelocity(trail, 1065)).toBeCloseTo(1, 1));
  it("is zero when the finger had stopped before lifting", () => expect(releaseVelocity(trail, 1300)).toBe(0));
  it("needs at least two samples", () => expect(releaseVelocity([{ x: 1, t: 1 }], 2)).toBe(0));
});
