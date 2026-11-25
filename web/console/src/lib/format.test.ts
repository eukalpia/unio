import { describe, expect, it } from "vitest";
import { formatBytes, formatDateTime, formatRate, percentOf } from "./format";

describe("format helpers", () => {
  it.each([
    [0, "0 B"],
    [1023, "1023 B"],
    [1536, "1.5 KiB"],
    [5 * 1024 ** 2, "5.0 MiB"],
    [120 * 1024 ** 3, "120 GiB"],
    [2 * 1024 ** 4, "2.0 TiB"],
  ])("formats %d bytes as %s", (bytes, text) => {
    expect(formatBytes(bytes)).toBe(text);
  });

  it("shows a dash for impossible sizes", () => {
    expect(formatBytes(-1)).toBe("—");
    expect(formatBytes(Number.NaN)).toBe("—");
  });

  it("formats a rate", () => {
    expect(formatRate(2048)).toBe("2.0 KiB/s");
  });

  it("shows a dash for an invalid timestamp", () => {
    expect(formatDateTime("not a date")).toBe("—");
    expect(formatDateTime("2026-10-01T10:00:00Z")).toMatch(/2026/);
  });

  it("clamps percentages", () => {
    expect(percentOf(50, 200)).toBe(25);
    expect(percentOf(300, 200)).toBe(100);
    expect(percentOf(1, 0)).toBe(0);
  });
});
