import { Link, useNavigate, useRouterState } from "@tanstack/react-router";
import { useQueryClient } from "@tanstack/react-query";
import { Database, LayoutDashboard, LogOut, Search, Server, ShieldCheck, Users } from "lucide-react";
import { useEffect, useState, type ComponentType, type ReactNode } from "react";
import { api } from "../lib/api/instance";
import { describeError } from "../lib/api/errors";
import { getSession, useSession } from "../lib/session/store";
import { notify } from "../lib/notify";
import { endSession } from "../features/auth/end-session";
import { cn } from "../lib/utils";
import { CommandPalette } from "./command-palette";
import { ThemeToggle } from "./chrome";
import { UnioMark, Wordmark } from "./logo";
import { Button } from "./ui/button";

interface NavItem {
  to: "/" | "/buckets" | "/users" | "/policies" | "/cluster";
  label: string;
  icon: ComponentType<{ className?: string; "aria-hidden"?: boolean }>;
}

const NAV: readonly NavItem[] = [
  { to: "/", label: "Dashboard", icon: LayoutDashboard },
  { to: "/buckets", label: "Buckets", icon: Database },
  { to: "/users", label: "Users", icon: Users },
  { to: "/policies", label: "Policies", icon: ShieldCheck },
  { to: "/cluster", label: "Cluster", icon: Server },
];


export function AppShell({ children }: { children: ReactNode }) {
  const session = useSession();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const location = useRouterState({ select: (state) => `${state.location.pathname}${state.location.searchStr}` });
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [signingOut, setSigningOut] = useState(false);

  // The session ends when the server rejects it, and the page moves to sign-in.
  useEffect(() => {
    if (session.status === "anonymous") {
      queryClient.clear();
      void navigate({ to: "/login", search: { redirect: location }, replace: true });
    }
  }, [session.status, location, navigate, queryClient]);

  // The session also ends at its expiry time, without waiting for a rejected request.
  useEffect(() => {
    if (session.status !== "signed-in") {
      return;
    }
    if (session.expiresAt === null) {
      return;
    }
    const remaining = Date.parse(session.expiresAt) - Date.now();
    const timer = setTimeout(() => {
      endSession();
    }, Math.max(0, remaining));
    return () => {
      clearTimeout(timer);
    };
  }, [session]);

  // Ctrl+K and Cmd+K open the command palette from anywhere in the shell.
  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setPaletteOpen((open) => !open);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => {
      window.removeEventListener("keydown", onKeyDown);
    };
  }, []);

  async function signOut(): Promise<void> {
    const current = getSession();
    if (current.status !== "signed-in" || signingOut) {
      return;
    }
    setSigningOut(true);
    try {
      await api.logout();
      endSession();
    } catch (error) {
      notify("error", describeError(error));
    } finally {
      setSigningOut(false);
    }
  }

  const signedIn = session.status === "signed-in" ? session : null;

  return (
    <div className="flex min-h-dvh flex-col md:flex-row">
      <a
        href="#main"
        className="bg-primary text-primary-foreground sr-only z-[70] rounded-md px-3 py-2 text-sm focus:not-sr-only focus:fixed focus:top-2 focus:left-2"
      >
        Skip to content
      </a>

      <aside className="border-border bg-card flex shrink-0 flex-col gap-4 border-b p-3 md:sticky md:top-0 md:h-dvh md:w-60 md:border-r md:border-b-0 md:p-4">
        <div className="flex items-center justify-between gap-2">
          <Link to="/" className="rounded-md" aria-label="Unio console dashboard">
            <Wordmark />
          </Link>
        </div>
        <nav aria-label="Main" className="-mx-1 flex gap-1 overflow-x-auto px-1 md:mx-0 md:flex-col md:overflow-visible">
          {NAV.map((item) => {
            const Icon = item.icon;
            return (
              <Link
                key={item.to}
                to={item.to}
                activeOptions={{ exact: item.to === "/" }}
                className={cn(
                  "text-muted-foreground hover:bg-accent hover:text-accent-foreground focus-visible:outline-ring flex shrink-0 items-center gap-2 rounded-md px-3 py-2 text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2",
                  "[&.active]:bg-accent [&.active]:text-foreground [&.active]:shadow-[inset_2px_0_0_var(--primary)]",
                )}
              >
                <Icon className="size-4" aria-hidden={true} />
                {item.label}
              </Link>
            );
          })}
        </nav>
        <p className="text-muted-foreground mt-auto hidden items-center gap-2 text-xs md:flex">
          <UnioMark className="size-4" small />
          Unio console
        </p>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        <header className="border-border bg-background/95 sticky top-0 z-30 flex h-14 items-center gap-2 border-b px-4 sm:px-6">
          <Button
            variant="outline"
            size="sm"
            className="text-muted-foreground min-w-0 justify-start font-normal"
            onClick={() => {
              setPaletteOpen(true);
            }}
            aria-keyshortcuts="Control+K Meta+K"
          >
            <Search aria-hidden="true" />
            <span className="truncate">Jump to…</span>
            <kbd className="border-border ml-2 hidden rounded border px-1 font-mono text-[0.7rem] sm:inline">Ctrl K</kbd>
          </Button>
          <div className="ml-auto flex min-w-0 items-center gap-2">
            {signedIn ? (
              <span className="hidden min-w-0 flex-col items-end text-right sm:flex">
                <span className="text-muted-foreground text-xs">Signed in as</span>
                <span className="max-w-[14rem] truncate font-mono text-xs" title={signedIn.accessKey}>
                  {signedIn.accessKey}
                </span>
              </span>
            ) : null}
            <ThemeToggle />
            <Button variant="outline" size="sm" onClick={() => void signOut()} disabled={signingOut}>
              <LogOut aria-hidden="true" />
              <span>Sign out</span>
            </Button>
          </div>
        </header>

        <main id="main" tabIndex={-1} className="min-w-0 flex-1 p-4 outline-none sm:p-6">
          {children}
        </main>
      </div>

      <CommandPalette open={paletteOpen} onOpenChange={setPaletteOpen} />
    </div>
  );
}
