/** A breadcrumb step: the label shown, and the prefix it navigates to. */
export interface PrefixCrumb {
  label: string;
  prefix: string;
}

/** Breaks "a/b/c/" into the crumbs "a/", "a/b/", "a/b/c/". The root is handled by the caller. */
export function prefixCrumbs(prefix: string): PrefixCrumb[] {
  const parts = prefix.split("/").filter((part) => part !== "");
  return parts.map((part, index) => ({
    label: part,
    prefix: `${parts.slice(0, index + 1).join("/")}/`,
  }));
}

/** The last path segment of a key, relative to the current prefix. */
export function displayName(key: string, prefix: string): string {
  const rest = key.startsWith(prefix) ? key.slice(prefix.length) : key;
  return rest.replace(/\/$/, "") || rest;
}

/** Normalises a prefix from user input: no leading slash, and a trailing slash when non-empty. */
export function normalisePrefix(input: string): string {
  const trimmed = input.replace(/^\/+/, "");
  if (trimmed === "") {
    return "";
  }
  return trimmed.endsWith("/") ? trimmed : `${trimmed}/`;
}

/** The object key for an uploaded file: the prefix plus the file name. */
export function uploadKey(prefix: string, fileName: string): string {
  return `${prefix}${fileName}`;
}

/** Keeps the first occurrence of each prefix, since pages can repeat them. */
export function uniquePrefixes(pages: readonly (readonly string[])[]): string[] {
  return [...new Set(pages.flat())];
}
