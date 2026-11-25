import { describe, expect, it } from "vitest";
import { mapWithConcurrency } from "./concurrency";

const pause = (ms: number): Promise<void> => new Promise((resolve) => setTimeout(resolve, ms));

describe("mapWithConcurrency", () => {
  it("keeps the input order and caps the calls in flight", async () => {
    let inFlight = 0;
    let peak = 0;
    const items = Array.from({ length: 12 }, (_, index) => index);

    const results = await mapWithConcurrency(items, 4, async (item) => {
      inFlight += 1;
      peak = Math.max(peak, inFlight);
      await pause(1);
      inFlight -= 1;
      return item * 2;
    });

    expect(peak).toBe(4);
    expect(results.map((result) => (result.status === "fulfilled" ? result.value : -1))).toEqual(
      items.map((item) => item * 2),
    );
  });

  it("records a rejection and still runs the rest", async () => {
    const results = await mapWithConcurrency([1, 2, 3], 2, (item) =>
      item === 2 ? Promise.reject(new Error("nope")) : Promise.resolve(item),
    );

    expect(results.map((result) => result.status)).toEqual(["fulfilled", "rejected", "fulfilled"]);
  });

  it("returns nothing for an empty list", async () => {
    await expect(mapWithConcurrency([], 4, () => Promise.resolve(1))).resolves.toEqual([]);
  });

  it("rejects a limit below one", async () => {
    await expect(mapWithConcurrency([1], 0, () => Promise.resolve(1))).rejects.toThrow(RangeError);
  });
});
