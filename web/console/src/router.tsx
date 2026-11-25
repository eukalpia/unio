import {
  createRootRouteWithContext,
  createRoute,
  createRouter,
  lazyRouteComponent,
  Outlet,
  redirect,
} from "@tanstack/react-router";
import { AppShell } from "./components/app-shell";
import { Toaster } from "./components/chrome";
import { NotFoundPage, RouteErrorPage } from "./components/route-states";
import { api } from "./lib/api/instance";
import { queryClient } from "./lib/query";
import { ensureSession } from "./lib/session/ensure";
import { getSession } from "./lib/session/store";
import { safeRedirect } from "./lib/validation";

// The console is served under /unio/console/. The router works with paths
// relative to that base, so `basepath` strips the prefix from every URL.
export const BASE_PATH = "/unio/console";

interface RouterContext {
  queryClient: typeof queryClient;
}

interface LoginSearch {
  redirect?: string;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: function RootLayout() {
    return (
      <>
        <Outlet />
        <Toaster />
      </>
    );
  },
  notFoundComponent: NotFoundPage,
  errorComponent: RouteErrorPage,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  validateSearch: (search: Record<string, unknown>): LoginSearch => ({
    redirect: typeof search["redirect"] === "string" ? search["redirect"] : undefined,
  }),
  beforeLoad: async ({ search }) => {
    // A valid session cookie is enough to skip the form, because GET session returns its CSRF value.
    const session = await ensureSession(api).catch(() => getSession());
    if (session.status === "signed-in") {
      // eslint-disable-next-line @typescript-eslint/only-throw-error -- TanStack Router expects redirect() to be thrown.
      throw redirect({ href: safeRedirect(search.redirect) });
    }
  },
  component: lazyRouteComponent(() => import("./features/auth/login-page"), "LoginPage"),
});

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "app",
  beforeLoad: async ({ location }) => {
    const session = await ensureSession(api);
    if (session.status !== "signed-in") {
      // eslint-disable-next-line @typescript-eslint/only-throw-error -- TanStack Router expects redirect() to be thrown.
      throw redirect({
        to: "/login",
        search: { redirect: `${location.pathname}${location.searchStr}` },
      });
    }
  },
  component: function AppLayout() {
    return (
      <AppShell>
        <Outlet />
      </AppShell>
    );
  },
});

const dashboardRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/",
  component: lazyRouteComponent(() => import("./features/dashboard/dashboard-page"), "DashboardPage"),
});

const bucketsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/buckets",
  component: lazyRouteComponent(() => import("./features/buckets/buckets-page"), "BucketsPage"),
});

const objectsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/buckets/$bucket",
  validateSearch: (search: Record<string, unknown>): { prefix: string } => ({
    prefix: typeof search["prefix"] === "string" ? search["prefix"].slice(0, 1024) : "",
  }),
  component: lazyRouteComponent(() => import("./features/objects/object-browser-page"), "ObjectBrowserPage"),
});

const usersRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/users",
  component: lazyRouteComponent(() => import("./features/users/users-page"), "UsersPage"),
});

const policiesRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/policies",
  component: lazyRouteComponent(() => import("./features/policies/policies-page"), "PoliciesPage"),
});

const clusterRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/cluster",
  component: lazyRouteComponent(() => import("./features/cluster/cluster-page"), "ClusterPage"),
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  appRoute.addChildren([dashboardRoute, bucketsRoute, objectsRoute, usersRoute, policiesRoute, clusterRoute]),
]);

export const router = createRouter({
  routeTree,
  basepath: BASE_PATH,
  context: { queryClient },
  defaultPreload: "intent",
  scrollRestoration: true,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
