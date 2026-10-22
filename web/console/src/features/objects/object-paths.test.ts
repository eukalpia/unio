import { describe, expect, it } from "vitest";
import { displayName, normalisePrefix, prefixCrumbs, uniquePrefixes, uploadKey } from "./object-paths";

describe("object paths", () => {
  it("builds breadcrumbs for each level of a prefix", () => {
    expect(prefixCrumbs("a/b/c/")).toEqual([
      { label: "a", prefix: "a/" },
      { label: "b", prefix: "a/b/" },
      { label: "c", prefix: "a/b/c/" },
    ]);
    expect(prefixCrumbs("")).toEqual([]);
  });

  it("shows the name of a key relative to the prefix", () => {
    expect(displayName("logs/2026/app.log", "logs/2026/")).toBe("app.log");
    expect(displayName("logs/2026/archive/", "logs/2026/")).toBe("archive");
  });

  it("normalises typed prefixes", () => {
    expect(normalisePrefix("/logs/2026")).toBe("logs/2026/");
    expect(normalisePrefix("logs/")).toBe("logs/");
    expect(normalisePrefix("")).toBe("");
  });

  it("joins the prefix and the file name for an upload", () => {
    expect(uploadKey("photos/", "cover.jpg")).toBe("photos/cover.jpg");
    expect(uploadKey("", "README.md")).toBe("README.md");
  });

  it("removes prefixes repeated across pages", () => {
    expect(uniquePrefixes([["a/", "b/"], ["b/", "c/"]])).toEqual(["a/", "b/", "c/"]);
  });
});
