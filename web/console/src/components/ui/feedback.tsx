import { AlertTriangle, CheckCircle2, Info } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";
import { cn } from "../../lib/utils";
import { percentOf } from "../../lib/format";

type AlertTone = "info" | "success" | "danger" | "warning";

const alertTones: Record<AlertTone, string> = {
  info: "border-border bg-muted text-foreground",
  success: "border-success/40 bg-success/10 text-foreground",
  danger: "border-destructive/50 bg-destructive/10 text-foreground",
  warning: "border-warning/50 bg-warning/10 text-foreground",
};

const alertIcons: Record<AlertTone, typeof Info> = {
  info: Info,
  success: CheckCircle2,
  danger: AlertTriangle,
  warning: AlertTriangle,
};

/** An inline message. Errors use role="alert" so screen readers announce them at once. */
export function Alert({
  tone = "info",
  title,
  children,
  className,
  ...props
}: { tone?: AlertTone; title?: string; children?: ReactNode; className?: string } & Omit<ComponentProps<"div">, "title">) {
  const Icon = alertIcons[tone];
  return (
    <div
      role={tone === "danger" ? "alert" : "status"}
      data-slot="alert"
      className={cn("flex gap-3 rounded-md border px-4 py-3 text-sm", alertTones[tone], className)}
      {...props}
    >
      <Icon className={cn("mt-0.5 size-4 shrink-0", tone === "danger" ? "text-destructive" : "text-muted-foreground")} aria-hidden="true" />
      <div className="flex min-w-0 flex-col gap-0.5">
        {title ? <p className="font-medium">{title}</p> : null}
        {children ? <div className="text-muted-foreground break-words">{children}</div> : null}
      </div>
    </div>
  );
}

/** A horizontal capacity or utilisation bar. It is a progressbar, so its value is announced. */
export function Meter({
  value,
  max,
  label,
  tone = "brand",
  className,
}: {
  value: number;
  max: number;
  label: string;
  tone?: "brand" | "warning" | "danger";
  className?: string;
}) {
  const percent = percentOf(value, max);
  const fill = tone === "danger" ? "bg-destructive" : tone === "warning" ? "bg-warning" : "bg-primary";
  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={Math.max(0, Math.round(max))}
      aria-valuenow={Math.round(Math.min(value, max))}
      aria-valuetext={`${percent.toFixed(0)} percent`}
      className={cn("bg-muted h-2 w-full overflow-hidden rounded-full", className)}
    >
      {/* Width is set through the CSSOM style property, which the Content Security Policy allows. */}
      <div className={cn("h-full rounded-full transition-[width] duration-300", fill)} style={{ width: `${percent}%` }} />
    </div>
  );
}

export function Skeleton({ className, ...props }: ComponentProps<"div">) {
  return <div aria-hidden="true" className={cn("bg-muted animate-pulse rounded-md", className)} {...props} />;
}

/** A centred block for a list with no items, or a page that has nothing to show yet. */
export function EmptyState({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <div className="border-border flex flex-col items-center gap-2 rounded-lg border border-dashed px-6 py-10 text-center">
      <p className="font-display text-base font-semibold">{title}</p>
      {description ? <p className="text-muted-foreground max-w-md text-sm">{description}</p> : null}
      {action ? <div className="mt-2">{action}</div> : null}
    </div>
  );
}
