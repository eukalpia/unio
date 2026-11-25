import { Dialog as DialogPrimitive } from "radix-ui";
import { X } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";
import { cn } from "../../lib/utils";

export const Dialog = DialogPrimitive.Root;
export const DialogTrigger = DialogPrimitive.Trigger;
export const DialogClose = DialogPrimitive.Close;

interface ContentProps extends ComponentProps<typeof DialogPrimitive.Content> {
  /** `sheet` slides in from the right and is used for drawers. */
  variant?: "dialog" | "sheet";
  /** Hides the corner close button. Use it when the dialog must be dismissed with a button. */
  hideClose?: boolean;
}

export function DialogContent({ className, children, variant = "dialog", hideClose = false, ...props }: ContentProps) {
  return (
    <DialogPrimitive.Portal>
      <DialogPrimitive.Overlay className="fixed inset-0 z-40 bg-ink/50 backdrop-blur-[1px] data-[state=closed]:animate-out data-[state=closed]:fade-out-0 data-[state=open]:animate-in data-[state=open]:fade-in-0 dark:bg-black/60" />
      <DialogPrimitive.Content
        data-slot="dialog-content"
        data-variant={variant}
        className={cn(
          "bg-card text-card-foreground fixed z-50 flex flex-col gap-4 border border-border shadow-lg outline-none",
          variant === "dialog"
            ? "top-1/2 left-1/2 max-h-[calc(100dvh-2rem)] w-[calc(100vw-2rem)] max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg p-6 sm:w-full"
            : "inset-y-0 right-0 h-dvh w-full max-w-xl overflow-y-auto border-l p-6 sm:max-w-2xl",
          className,
        )}
        {...props}
      >
        {children}
        {hideClose ? null : (
          <DialogPrimitive.Close className="text-muted-foreground hover:text-foreground focus-visible:outline-ring absolute top-4 right-4 cursor-pointer rounded-sm p-1 focus-visible:outline-2 focus-visible:outline-offset-2">
            <X className="size-4" aria-hidden="true" />
            <span className="sr-only">Close</span>
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Content>
    </DialogPrimitive.Portal>
  );
}

export function DialogHeader({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("flex flex-col gap-1.5 pr-8", className)} {...props} />;
}

export function DialogFooter({ className, ...props }: ComponentProps<"div">) {
  return <div className={cn("mt-2 flex flex-col-reverse gap-2 sm:flex-row sm:justify-end", className)} {...props} />;
}

export function DialogTitle({ className, ...props }: ComponentProps<typeof DialogPrimitive.Title>) {
  return <DialogPrimitive.Title className={cn("font-display text-lg font-semibold", className)} {...props} />;
}

export function DialogDescription({ className, ...props }: ComponentProps<typeof DialogPrimitive.Description>) {
  return <DialogPrimitive.Description className={cn("text-muted-foreground text-sm", className)} {...props} />;
}

/** A labelled region for a dialog body, so screen readers announce its sections. */
export function DialogSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2" aria-label={title}>
      {children}
    </section>
  );
}
