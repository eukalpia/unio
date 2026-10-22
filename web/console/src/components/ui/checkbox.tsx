import type { InputHTMLAttributes, Ref } from "react";
import { cn } from "../../lib/utils";

export interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "onChange" | "checked"> {
  checked: boolean;
  onChange: (checked: boolean) => void;
  ref?: Ref<HTMLInputElement>;
}

/** A native checkbox, so keyboard and screen reader behaviour comes from the browser. */
export function Checkbox({ checked, onChange, className, ref, ...props }: CheckboxProps) {
  return (
    <input
      ref={ref}
      type="checkbox"
      checked={checked}
      onChange={(event) => {
        onChange(event.target.checked);
      }}
      className={cn(
        "border-input bg-card accent-primary size-4 cursor-pointer rounded-sm disabled:cursor-not-allowed disabled:opacity-50 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring",
        className,
      )}
      {...props}
    />
  );
}
