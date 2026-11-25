import { useSyncExternalStore } from "react";

export type ToastTone = "success" | "error" | "info";

export interface Toast {
  id: number;
  tone: ToastTone;
  message: string;
}

const AUTO_DISMISS_MS = 6000;
const MAX_VISIBLE = 4;

let toasts: readonly Toast[] = [];
let nextId = 1;
const timers = new Map<number, ReturnType<typeof setTimeout>>();
const listeners = new Set<() => void>();

function publish(next: readonly Toast[]): void {
  toasts = next;
  for (const listener of listeners) {
    listener();
  }
}

/** Shows a short status message in the live region. Messages must be plain text. */
export function notify(tone: ToastTone, message: string): number {
  const id = nextId++;
  const next = [...toasts, { id, tone, message }].slice(-MAX_VISIBLE);
  publish(next);
  timers.set(
    id,
    setTimeout(() => {
      dismissToast(id);
    }, AUTO_DISMISS_MS),
  );
  return id;
}

export function dismissToast(id: number): void {
  const timer = timers.get(id);
  if (timer !== undefined) {
    clearTimeout(timer);
    timers.delete(id);
  }
  publish(toasts.filter((toast) => toast.id !== id));
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

export function useToasts(): readonly Toast[] {
  return useSyncExternalStore(subscribe, () => toasts, () => toasts);
}
