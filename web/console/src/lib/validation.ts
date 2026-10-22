import type { PolicyDocument } from "./api/types";

// Client-side checks mirror the S3 bucket naming rules. The server stays the
// authority, and its message is shown when a name passes here but is refused.

const IPV4_LIKE = /^\d{1,3}(\.\d{1,3}){3}$/;
const BUCKET_CHARS = /^[a-z0-9.-]+$/;
const RESERVED_PREFIXES = ["xn--", "sthree-", "amzn-s3-demo-"];
const RESERVED_SUFFIXES = ["-s3alias", "--ol-s3", ".mrap", "--x-s3"];

/** Returns the first rule the bucket name breaks, or null when the name is valid. */
export function bucketNameError(name: string): string | null {
  if (name.length < 3 || name.length > 63) {
    return "Use between 3 and 63 characters.";
  }
  if (!BUCKET_CHARS.test(name)) {
    return "Use only lowercase letters, numbers, dots (.) and hyphens (-).";
  }
  if (!/^[a-z0-9]/.test(name) || !/[a-z0-9]$/.test(name)) {
    return "Start and end with a lowercase letter or a number.";
  }
  if (name.includes("..")) {
    return "Do not put two dots next to each other.";
  }
  if (IPV4_LIKE.test(name)) {
    return "Do not format the name as an IP address.";
  }
  if (RESERVED_PREFIXES.some((prefix) => name.startsWith(prefix))) {
    return "Do not start with xn--, sthree- or amzn-s3-demo-.";
  }
  if (RESERVED_SUFFIXES.some((suffix) => name.endsWith(suffix))) {
    return "Do not end with -s3alias, --ol-s3, .mrap or --x-s3.";
  }
  if (name === "unio") {
    return "The name unio is reserved.";
  }
  return null;
}

const IAM_NAME = /^[A-Za-z0-9+=,.@_-]{1,64}$/;
const POLICY_NAME = /^[A-Za-z0-9+=,.@_-]{1,128}$/;

/** Names of users, which are also used in URL paths. */
export function userNameError(name: string): string | null {
  return IAM_NAME.test(name) ? null : "Use 1 to 64 letters, numbers or any of + = , . @ _ -.";
}

export function policyNameError(name: string): string | null {
  return POLICY_NAME.test(name) ? null : "Use 1 to 128 letters, numbers or any of + = , . @ _ -.";
}

/** Requests are limited to 64 KiB. This leaves room for the JSON wrapper. */
export const MAX_POLICY_BYTES = 60 * 1024;

export type PolicyParseResult =
  | { ok: true; document: PolicyDocument; pretty: string }
  | { ok: false; message: string };

/** Parses a policy document typed by the user. It must be a JSON object. */
export function parsePolicyText(text: string): PolicyParseResult {
  if (text.trim() === "") {
    return { ok: false, message: "Enter a JSON policy document." };
  }
  if (new TextEncoder().encode(text).byteLength > MAX_POLICY_BYTES) {
    return { ok: false, message: "The policy is too large. The limit is 60 KiB." };
  }
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch (error) {
    const reason = error instanceof SyntaxError ? error.message : "invalid JSON";
    return { ok: false, message: `Invalid JSON: ${reason}` };
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    return { ok: false, message: "The policy must be a JSON object." };
  }
  const document = value as PolicyDocument;
  return { ok: true, document, pretty: JSON.stringify(document, null, 2) };
}

/**
 * Accepts only in-app paths for post-login redirects, so the login page cannot
 * be used to send the user to another site.
 */
export function safeRedirect(value: unknown, fallback = "/"): string {
  if (typeof value !== "string" || value.length > 2048) {
    return fallback;
  }
  if (!value.startsWith("/") || value.startsWith("//") || value.startsWith("/\\")) {
    return fallback;
  }
  if (hasControlOrBackslash(value) || value.startsWith("/login")) {
    return fallback;
  }
  return value;
}

function hasControlOrBackslash(value: string): boolean {
  for (let index = 0; index < value.length; index += 1) {
    const code = value.charCodeAt(index);
    if (code < 0x20 || code === 0x5c) {
      return true;
    }
  }
  return false;
}
