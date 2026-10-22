import { Label as LabelPrimitive } from "radix-ui";
import type { InputHTMLAttributes, Ref, SelectHTMLAttributes, TextareaHTMLAttributes } from "react";
import { cn } from "../../lib/utils";

const fieldBase =
  "w-full min-w-0 rounded-md border border-input bg-card px-3 text-sm text-foreground shadow-xs placeholder:text-muted-foreground focus-visible:outline-2 focus-visible:outline-offset-1 focus-visible:outline-ring disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:outline-destructive";

export function Label({ className, ...props }: LabelPrimitive.LabelProps) {
  return (
    <LabelPrimitive.Root
      data-slot="label"
      className={cn("text-sm leading-none font-medium text-foreground select-none", className)}
      {...props}
    />
  );
}

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  ref?: Ref<HTMLInputElement>;
}

export function Input({ className, type = "text", ref, ...props }: InputProps) {
  return (
    <input
      ref={ref}
      type={type}
      data-slot="input"
      className={cn(fieldBase, "h-9 py-1", className)}
      {...props}
    />
  );
}

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  ref?: Ref<HTMLTextAreaElement>;
}

export function Textarea({ className, ref, ...props }: TextareaProps) {
  return <textarea ref={ref} data-slot="textarea" className={cn(fieldBase, "py-2", className)} {...props} />;
}

export interface SelectProps extends SelectHTMLAttributes<HTMLSelectElement> {
  ref?: Ref<HTMLSelectElement>;
}

/** A native select, which is fully accessible and keeps the bundle small. */
export function NativeSelect({ className, ref, ...props }: SelectProps) {
  return <select ref={ref} data-slot="select" className={cn(fieldBase, "h-9 py-1", className)} {...props} />;
}

/** Shows an error under a field and links it to the field with aria-describedby. */
export function FieldError({ id, message }: { id: string; message: string | null | undefined }) {
  if (!message) {
    return null;
  }
  return (
    <p id={id} className="text-destructive text-xs leading-snug">
      {message}
    </p>
  );
}
