// Checks that the production build runs under the Content Security Policy that
// the node sends for /unio/console/ (see the "Console hosting" section of the contract).
//
// 1. Static checks on dist/ always run: no inline script or style, no eval, no
//    data: fonts, and no style attributes in markup.
// 2. A tiny server sends the policy and answers the session endpoint with 401, so the
//    sign-in page renders. If CONSOLE_BROWSER names a Chromium-based browser, it loads the
//    page headless and fails when the browser logs a Content Security Policy violation.
//
// Usage: node scripts/check-csp.mjs [distDir]
//   CONSOLE_BROWSER=/path/to/chrome CONSOLE_BROWSER_PROFILE=/tmp/profile node scripts/check-csp.mjs
import { spawn } from "node:child_process";
import { createServer } from "node:http";
import { closeSync, existsSync, openSync, readdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { extname, join, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const CSP = [
  "default-src 'self'",
  "script-src 'self'",
  "style-src 'self'",
  "img-src 'self' data:",
  "connect-src 'self'",
  "font-src 'self'",
  "frame-ancestors 'none'",
  "base-uri 'none'",
  "form-action 'self'",
].join("; ");

const BASE = "/unio/console/";
const root = resolve(fileURLToPath(new URL("..", import.meta.url)));
const dist = resolve(process.argv[2] ?? join(root, "dist"));
const failures = [];

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".json": "application/json",
  ".png": "image/png",
};

function walk(dir) {
  return readdirSync(dir).flatMap((entry) => {
    const path = join(dir, entry);
    return statSync(path).isDirectory() ? walk(path) : [path];
  });
}

function check(condition, message) {
  if (!condition) {
    failures.push(message);
  }
}

// --- 1. static checks -------------------------------------------------------

if (!existsSync(join(dist, "index.html"))) {
  console.error(`No build found at ${dist}. Run npm run build first.`);
  process.exit(1);
}

const html = readFileSync(join(dist, "index.html"), "utf8");
for (const match of html.matchAll(/<script\b([^>]*)>/gi)) {
  check(/\ssrc=/.test(match[1] ?? ""), `inline <script> in index.html: ${match[0]}`);
}
check(!/<style\b/i.test(html), "a <style> element is in index.html");
check(!/\sstyle="/i.test(html), "a style attribute is in index.html");

const files = walk(dist);
for (const file of files) {
  const ext = extname(file);
  if (ext !== ".js" && ext !== ".css" && ext !== ".html") {
    continue;
  }
  const text = readFileSync(file, "utf8");
  const name = file.slice(dist.length + 1).split(sep).join("/");
  check(!/\beval\s*\(/.test(text), `eval( found in ${name}`);
  check(!/new Function\s*\(/.test(text), `new Function( found in ${name}`);
  if (ext === ".css") {
    check(!/url\(\s*["']?data:font/i.test(text), `a data: font is referenced in ${name}`);
    check(!/@import\s+url\(\s*["']?https?:/i.test(text), `an external stylesheet is imported in ${name}`);
  }
}

// --- 2. run under the policy --------------------------------------------------

const server = createServer((request, response) => {
  const url = new URL(request.url ?? "/", "http://127.0.0.1");
  const headers = {
    "Content-Security-Policy": CSP,
    "X-Content-Type-Options": "nosniff",
    "Referrer-Policy": "no-referrer",
  };
  if (url.pathname === "/unio/admin/v1/session") {
    response.writeHead(401, { ...headers, "Content-Type": "application/json" });
    response.end(JSON.stringify({ code: "AccessDenied", message: "Access denied.", requestId: "csp-check" }));
    return;
  }
  if (!url.pathname.startsWith(BASE)) {
    response.writeHead(404, headers);
    response.end();
    return;
  }
  const relative = decodeURIComponent(url.pathname.slice(BASE.length));
  let path = resolve(dist, relative);
  if (!path.startsWith(dist) || !existsSync(path) || statSync(path).isDirectory()) {
    path = join(dist, "index.html");
  }
  const cache = path.endsWith("index.html") ? "no-cache" : "public, max-age=31536000, immutable";
  response.writeHead(200, { ...headers, "Content-Type": MIME[extname(path)] ?? "application/octet-stream", "Cache-Control": cache });
  response.end(readFileSync(path));
});

await new Promise((ready) => server.listen(0, "127.0.0.1", ready));
const address = server.address();
const port = typeof address === "object" && address ? address.port : 0;
const pageUrl = `http://127.0.0.1:${port}${BASE}`;

try {
  const browser = process.env["CONSOLE_BROWSER"];
  if (!browser) {
    console.log("CONSOLE_BROWSER is not set: skipped the headless browser run.");
  } else {
    const profile = process.env["CONSOLE_BROWSER_PROFILE"] ?? join(tmpdir(), "unio-csp-profile");
    // Browser children keep inherited pipes open, so output goes to files instead of pipes.
    const stdoutPath = join(tmpdir(), `unio-csp-${process.pid}.out`);
    const stderrPath = join(tmpdir(), `unio-csp-${process.pid}.err`);
    const quote = (value) => `'${String(value).replace(/'/g, "'\\''")}'`;
    const args = [
      "--headless=new",
      "--disable-gpu",
      "--no-sandbox",
      "--no-first-run",
      `--user-data-dir=${profile}`,
      "--enable-logging=stderr",
      "--v=0",
      "--virtual-time-budget=8000",
      "--dump-dom",
      pageUrl,
    ];
    // The shell performs the redirection, which lets the browser exit cleanly.
    const command = `${[browser, ...args].map(quote).join(" ")} > ${quote(stdoutPath)} 2> ${quote(stderrPath)}`;
    // Asynchronous, so the server above keeps answering while the browser loads the page.
    const runOnce = () =>
      new Promise((resolveRun) => {
        const child = spawn("/bin/sh", ["-c", command], { stdio: ["inherit", "ignore", "ignore"] });
        const timer = setTimeout(() => child.kill("SIGKILL"), 120_000);
        child.on("exit", (status, signal) => {
          clearTimeout(timer);
          resolveRun({ status, signal });
        });
      });
    // WSL forwards a freshly bound localhost port to Windows a moment later. A refused
    // connection is a transport race, not a result, so the run is retried up to three times.
    let run;
    let dom = "";
    let logText = "";
    for (let attempt = 1; attempt <= 3; attempt += 1) {
      await new Promise((ready) => setTimeout(ready, 1000));
      run = await runOnce();
      dom = readFileSync(stdoutPath, "utf8");
      logText = readFileSync(stderrPath, "utf8");
      if (!/ERR_CONNECTION_REFUSED/.test(logText)) {
        break;
      }
      console.error(`attempt ${attempt}: the browser could not reach the check server; retrying`);
    }
    rmSync(stdoutPath, { force: true });
    rmSync(stderrPath, { force: true });
    const violations = logText
      .split("\n")
      .filter((line) => /Content Security Policy|Refused to (load|execute|apply|connect|create)/i.test(line));
    check(run.status === 0, `the browser did not exit cleanly (status ${run.status ?? run.signal ?? "none"})`);
    check(violations.length === 0, `the browser reported CSP violations:\n  ${violations.join("\n  ")}`);
    check(/data-testid="login-card"/.test(dom), "the sign-in page did not render in the browser");
    if (!/data-testid="login-card"/.test(dom)) {
      console.error(`DOM starts with: ${dom.replace(/\s+/g, " ").slice(0, 400)}`);
      const interesting = logText.split("\n").filter((line) => /CONSOLE|ERROR|Refused|Page load/.test(line) && !/Missing value|policy manager/.test(line));
      console.error(`browser log (${interesting.length} lines): ${interesting.slice(0, 8).join(" / ")}`);
    }
    check(!/Uncaught/i.test(logText), "the browser reported an uncaught script error");
    console.log(`headless browser run: ${violations.length} CSP violation(s), exit ${run.status ?? "none"}`);
  }
} finally {
  server.close();
}

if (failures.length > 0) {
  console.error("CSP check failed:");
  for (const failure of failures) {
    console.error(`  - ${failure}`);
  }
  process.exit(1);
}
console.log(`CSP check passed for ${files.length} built files under: ${CSP}`);
