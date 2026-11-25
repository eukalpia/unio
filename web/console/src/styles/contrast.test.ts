// @vitest-environment node
import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

// Checks the colour tokens in index.css against WCAG 2.1 AA. Text needs 4.5:1,
// and control boundaries and focus rings need 3:1 (SC 1.4.11).

const here = dirname(fileURLToPath(import.meta.url));
const css = readFileSync(resolve(here, "../index.css"), "utf8");

function tokens(selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {`);
  if (start < 0) {
    throw new Error(`selector ${selector} not found`);
  }
  const body = css.slice(start, css.indexOf("}", start));
  const entries: Record<string, string> = {};
  for (const match of body.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-fA-F]{6});/g)) {
    entries[match[1] ?? ""] = (match[2] ?? "").toLowerCase();
  }
  return entries;
}

function luminance(hex: string): number {
  const channels = [1, 3, 5].map((offset) => {
    const value = parseInt(hex.slice(offset, offset + 2), 16) / 255;
    return value <= 0.03928 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4;
  });
  const [r = 0, g = 0, b = 0] = channels;
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [hi = 0, lo = 0] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

const TEXT_PAIRS: [string, string][] = [
  ["foreground", "background"],
  ["muted-foreground", "background"],
  ["muted-foreground", "muted"],
  ["card-foreground", "card"],
  ["accent-foreground", "accent"],
  ["primary-foreground", "primary"],
  ["primary", "background"],
  ["destructive", "background"],
  ["destructive-foreground", "destructive"],
  ["success", "background"],
  ["warning", "background"],
];

const NON_TEXT_PAIRS: [string, string][] = [
  ["input", "background"],
  ["ring", "background"],
  ["brand", "background"],
  ["chart-1", "background"],
  ["chart-2", "background"],
];

describe.each([
  [":root", "light"],
  [".dark", "dark"],
])("colour tokens in %s (%s theme)", (selector) => {
  const palette = tokens(selector);

  it.each(TEXT_PAIRS)("text %s on %s reaches 4.5:1", (text, background) => {
    const value = contrast(palette[text] ?? "", palette[background] ?? "");
    expect(value).toBeGreaterThanOrEqual(4.5);
  });

  it.each(NON_TEXT_PAIRS)("%s against %s reaches 3:1", (part, background) => {
    const value = contrast(palette[part] ?? "", palette[background] ?? "");
    expect(value).toBeGreaterThanOrEqual(3);
  });
});
