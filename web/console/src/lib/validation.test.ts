import { describe, expect, it } from "vitest";
import { bucketNameError, parsePolicyText, policyNameError, safeRedirect, userNameError } from "./validation";

describe("bucketNameError", () => {
  it.each([
    ["ab", "Use between 3 and 63 characters."],
    ["a".repeat(64), "Use between 3 and 63 characters."],
    ["My-Bucket", "Use only lowercase letters, numbers, dots (.) and hyphens (-)."],
    ["bucket_one", "Use only lowercase letters, numbers, dots (.) and hyphens (-)."],
    ["-bucket", "Start and end with a lowercase letter or a number."],
    ["bucket-", "Start and end with a lowercase letter or a number."],
    ["my..bucket", "Do not put two dots next to each other."],
    ["192.168.5.4", "Do not format the name as an IP address."],
    ["xn--bucket", "Do not start with xn--, sthree- or amzn-s3-demo-."],
    ["amzn-s3-demo-x", "Do not start with xn--, sthree- or amzn-s3-demo-."],
    ["bucket-s3alias", "Do not end with -s3alias, --ol-s3, .mrap or --x-s3."],
    ["unio", "The name unio is reserved."],
  ])("rejects %s", (name, message) => {
    expect(bucketNameError(name)).toBe(message);
  });

  it.each(["logs", "media-2026", "backup.eu.west", "a1b"])("accepts %s", (name) => {
    expect(bucketNameError(name)).toBeNull();
  });
});

describe("name checks", () => {
  it("checks user and policy names", () => {
    expect(userNameError("alice")).toBeNull();
    expect(userNameError("bad name")).not.toBeNull();
    expect(userNameError("")).not.toBeNull();
    expect(policyNameError("ReadOnly+media")).toBeNull();
    expect(policyNameError("a/b")).not.toBeNull();
  });
});

describe("parsePolicyText", () => {
  it("accepts a JSON object and pretty prints it", () => {
    const result = parsePolicyText('{"Statement":[]}');
    expect(result).toMatchObject({ ok: true, document: { Statement: [] } });
    expect(result.ok && result.pretty).toContain('\n  "Statement"');
  });

  it("explains invalid JSON", () => {
    const result = parsePolicyText('{"Statement": ');
    expect(result.ok).toBe(false);
    expect(!result.ok && result.message.startsWith("Invalid JSON:")).toBe(true);
  });

  it.each([
    ["", "Enter a JSON policy document."],
    ["[]", "The policy must be a JSON object."],
    ["null", "The policy must be a JSON object."],
    ["42", "The policy must be a JSON object."],
  ])("rejects %j", (text, message) => {
    expect(parsePolicyText(text)).toEqual({ ok: false, message });
  });

  it("rejects a document above the size limit", () => {
    const text = JSON.stringify({ Statement: "x".repeat(61 * 1024) });
    expect(parsePolicyText(text)).toMatchObject({ ok: false });
  });
});

describe("safeRedirect", () => {
  it.each([
    ["/buckets/logs?prefix=2026%2F", "/buckets/logs?prefix=2026%2F"],
    ["/users", "/users"],
  ])("keeps in-app path %s", (value, expected) => {
    expect(safeRedirect(value)).toBe(expected);
  });

  it.each([
    "https://evil.example/",
    "//evil.example/",
    "/\\evil.example",
    "/login?redirect=/",
    "users",
    "/bad\u0007path",
    undefined,
  ])("falls back to the dashboard for %j", (value) => {
    expect(safeRedirect(value)).toBe("/");
  });
});
