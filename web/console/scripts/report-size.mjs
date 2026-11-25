// Prints raw and gzip sizes of the production build, so bundle growth shows up in review.
// Usage: node scripts/report-size.mjs [distDir]
import { gzipSync } from "node:zlib";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(fileURLToPath(new URL("..", import.meta.url)));
const dist = resolve(process.argv[2] ?? join(root, "dist"));

function walk(dir) {
  return readdirSync(dir).flatMap((entry) => {
    const path = join(dir, entry);
    return statSync(path).isDirectory() ? walk(path) : [path];
  });
}

const files = walk(dist)
  .filter((path) => /\.(js|css|html|svg|woff2)$/.test(path))
  .sort((a, b) => a.localeCompare(b));

const rows = files.map((path) => {
  const raw = readFileSync(path);
  return {
    file: relative(dist, path).split("\\").join("/"),
    raw: raw.length,
    gzip: gzipSync(raw, { level: 9 }).length,
  };
});

const kib = (bytes) => `${(bytes / 1024).toFixed(2)} KiB`;
const width = Math.max(...rows.map((row) => row.file.length), 4);
console.log(`${"file".padEnd(width)}  ${"raw".padStart(12)}  ${"gzip".padStart(12)}`);
for (const row of rows) {
  console.log(`${row.file.padEnd(width)}  ${kib(row.raw).padStart(12)}  ${kib(row.gzip).padStart(12)}`);
}

const scripts = rows.filter((row) => row.file.endsWith(".js") && row.file.startsWith("assets/"));
const styles = rows.filter((row) => row.file.endsWith(".css"));
const sum = (list, key) => list.reduce((total, row) => total + row[key], 0);
console.log("");
console.log(`JavaScript (all chunks): ${kib(sum(scripts, "raw"))} raw, ${kib(sum(scripts, "gzip"))} gzip`);
console.log(`CSS:                     ${kib(sum(styles, "raw"))} raw, ${kib(sum(styles, "gzip"))} gzip`);
