# Unio web console

The console is a single-page app for administering a Unio cluster. It talks only to
the Admin API under `/unio/admin/v1/` on the S3 port, and the node serves it at
`/unio/console/`.

Stack: React 19, TypeScript (strict), Vite, Tailwind CSS v4, TanStack Router and
Query, TanStack Virtual, Radix primitives, and MSW for mocks. Versions are pinned
exactly in `package.json`, and `package-lock.json` is committed.

## Pages

| Route | What it does |
|---|---|
| `/login` | Access key and secret key sign-in. Generic failure text, a hold for the `Retry-After` period after rate limiting, and the secret is cleared on submit. |
| `/` | Dashboard: requests, errors, p99 latency and deletions (polled every 5 s), a throughput chart over ten minutes, capacity, node and drive health, and alerts. |
| `/buckets` | List, filter, create (S3 naming rules checked as you type) and delete (type the name to confirm). |
| `/buckets/$bucket` | Object browser: breadcrumbs, a virtualised list paged by token, upload by drag and drop or file picker (presigned PUT, four at a time, progress and cancel), download (presigned GET), delete one or many, and a details drawer. |
| `/users` | Create, enable, disable and delete users. Manage access keys (the secret is shown once) and attach or detach policies. |
| `/policies` | List, JSON editor with a parse check, create, update and delete. While the node answers 501 for the policy API (named policies are not implemented yet), the page says so, and the users page keeps showing the policy names each user has. |
| `/cluster` | Nodes, erasure sets, and each drive with its status and capacity. |

The app shell has a sidebar, a top bar with the access key and sign-out, light and
dark themes, and a command palette on Ctrl+K (or Cmd+K) to jump to pages and buckets.

## Develop

```sh
npm ci
npm run dev          # http://localhost:5173/unio/console/ with the in-browser mock server
npm run dev:live     # proxies /unio/admin to a running node: UNIO_DEV_API=https://127.0.0.1:9000 npm run dev:live
```

Mock sign-in (development only, never used by a node):

- access key: `UNIOROOTDEV0000000001`
- secret key: `dev-only-secret-for-mock-server`

The mock server lives in `src/mocks/`. Its handlers follow the Admin API contract,
including the CSRF header check, strict JSON bodies, the root user protections, and
a 10-attempts-per-minute sign-in limit with `Retry-After`. The named-policy endpoints
answer 501, as the node does until the IAM change lands, so the Policies page shows its
not-available state in the mock too. Mock data is in `src/mocks/state.ts`,
including a prefix with 2,500 objects, for exercising pagination. `VITE_MOCKS=true`
is set in `.env.development`. Production builds do not set it, so the mock code is
not bundled.

## Test and check

```sh
npm run typecheck    # tsc, strict, for the app and the build config
npm run lint         # eslint with typescript-eslint (strict, type-checked)
npm test             # Vitest with jsdom: API client, CSRF, sign-in form, uploads, validation, charts, colour contrast
npm run build        # typecheck, then vite build, then the gzip size report
npm run check:csp    # checks dist/ against the console CSP, optionally in a real browser
npm audit            # no high or critical findings is the bar
```

`npm run check:csp` always runs static checks on `dist/`: no inline script or
style, no `eval`, no data: fonts, and no `style` attributes in markup. If
`CONSOLE_BROWSER` names a Chromium-based browser, it also serves `dist/` with the
contract CSP and loads the page headless. It fails on any Content Security Policy
violation logged by the browser, or if the sign-in page does not render:

```sh
CONSOLE_BROWSER="/path/to/chrome" CONSOLE_BROWSER_PROFILE=/tmp/unio-csp npm run check:csp
```

## Build and embed

```sh
npm run build        # writes dist/
```

The Go package `internal/console` embeds `internal/console/assets/` (`//go:embed
all:assets`). `make console` builds this app and copies `dist/` into that
directory, so the next `go build` embeds the console. Without it, the committed
placeholder page is served and says the console was not built.

Docker: the Node stage in `deploy/docker/Dockerfile` is optional. The default build
skips it. To include the console in the image, pass
`--build-arg CONSOLE_STAGE=console-build`, which needs `node:22-bookworm-slim` to be
available.

Response headers (set by the node, listed here so the app is built to match):
`Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self';
img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none';
base-uri 'none'; form-action 'self'`.

## Design notes

- **Under the CSP.** There are no inline scripts and no runtime `<style>` tags.
  Styling is Tailwind classes compiled into one CSS file. The few dynamic widths
  (bars, the chart tooltip) are set through the CSS object model, which the policy
  allows. The theme is set before first paint by `public/theme-init.js`, an external
  script. Fonts are self-hosted, from `@fontsource-variable/space-grotesk`.
- **CSRF and reloads.** `GET session` returns the CSRF value for a cookie-authenticated
  request. On start-up the console calls it: 200 keeps the session and its CSRF value,
  and 401 shows the sign-in page. The CSRF value is kept in memory only. It is never
  written to localStorage or sessionStorage, and a test checks that both stay empty.
- **Errors from the node.** 401 means the session is missing or invalid, and the console
  signs out. 403 `AccessDenied` is a refusal (CSRF or permission). 429 `SlowDown` carries
  `Retry-After`, which sets the sign-in hold. 413 is a body that is too large. 409
  `RootUserProtected` and `BucketNotEmpty` show the node's message. 501 means the feature
  is not available on this node yet.
- **The secret key** is read from its field once, sent in the request body and
  cleared. It is never put in the store, the query cache or the URL. The new secret
  for an access key is held in component state, which is cleared when its dialog closes.
- **Deleting objects.** The contract removes one key per request, so a multi-select
  delete sends the requests four at a time.
- **Uploads** use `POST buckets/{name}/presign` and then a direct PUT to the presigned
  URL, through XMLHttpRequest, which reports progress. The queue is one console-wide
  instance with a concurrency of four.
- **Charts** are inline SVG, with no chart library. The two series use the validated
  categorical pair, the legend is always shown, and the same readout is available by
  pointer, by keyboard, and as a table.
- **Accessibility.** Targets are WCAG 2.1 AA. Text colours and control boundaries are
  checked against the brand palette in `src/styles/contrast.test.ts`. The brand blue
  is one shade darker for button text, because white on the exact `#2F6BFF` measures
  4.499:1. Layouts work from 360 px, and tables scroll inside their own container.
