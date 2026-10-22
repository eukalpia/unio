import { Moon, Sun, X } from "lucide-react";
import type { ReactNode } from "react";
import { dismissToast, useToasts, type ToastTone } from "../lib/notify";
import { setTheme, useTheme } from "../lib/theme";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";

export function ThemeToggle() {
  const theme = useTheme();
  const next = theme === "dark" ? "light" : "dark";
  return (
    <Button
      variant="ghost"
      size="icon-sm"
      aria-label={`Switch to the ${next} theme`}
      title={`Switch to the ${next} theme`}
      onClick={() => {
        setTheme(next);
      }}
    >
      {theme === "dark" ? <Sun aria-hidden="true" /> : <Moon aria-hidden="true" />}
    </Button>
  );
}

const toneClass: Record<ToastTone, string> = {
  success: "border-l-success",
  error: "border-l-destructive",
  info: "border-l-primary",
};

/** Shows status messages. The region is a polite live region, so messages are read without moving focus. */
export function Toaster() {
  const toasts = useToasts();
  return (
    <div
      className="pointer-events-none fixed inset-x-0 bottom-0 z-[60] flex flex-col items-end gap-2 p-4 sm:left-auto sm:w-96"
      role="region"
      aria-label="Notifications"
    >
      <div role="status" aria-live="polite" className="flex w-full flex-col gap-2">
        {toasts.map((toast) => (
          <div
            key={toast.id}
            className={cn(
              "bg-card text-card-foreground pointer-events-auto flex items-start gap-3 rounded-md border border-l-4 border-border p-3 text-sm shadow-lg",
              toneClass[toast.tone],
            )}
          >
            <p className="min-w-0 flex-1 break-words">{toast.message}</p>
            <Button variant="ghost" size="icon-sm" aria-label="Dismiss notification" onClick={() => { dismissToast(toast.id); }}>
              <X aria-hidden="true" />
            </Button>
          </div>
        ))}
      </div>
    </div>
  );
}

export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
      <div className="flex min-w-0 flex-col gap-1">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {description ? <div className="text-muted-foreground text-sm">{description}</div> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </header>
  );
}
