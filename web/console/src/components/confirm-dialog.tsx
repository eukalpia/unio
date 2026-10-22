import { useId, useState, type SyntheticEvent } from "react";
import { Button } from "./ui/button";
import { Alert } from "./ui/feedback";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "./ui/dialog";
import { Input, Label } from "./ui/form";

interface BaseProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description: string;
  confirmLabel: string;
  pending?: boolean;
  error?: string | null;
  onConfirm: () => void;
}

/** A confirmation for actions that can be undone or are low risk. */
export function ConfirmDialog(props: BaseProps) {
  return (
    <Dialog open={props.open} onOpenChange={props.onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{props.title}</DialogTitle>
          <DialogDescription>{props.description}</DialogDescription>
        </DialogHeader>
        {props.error ? <Alert tone="danger">{props.error}</Alert> : null}
        <DialogFooter>
          <Button variant="outline" onClick={() => { props.onOpenChange(false); }}>
            Cancel
          </Button>
          <Button variant="destructive" disabled={props.pending} onClick={props.onConfirm}>
            {props.pending ? "Working…" : props.confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/**
 * A destructive confirmation that needs the exact name typed first. It guards
 * deletes of buckets and users, which cannot be recovered.
 */
export function TypeToConfirmDialog(props: BaseProps & { expected: string; fieldLabel: string }) {
  const inputId = useId();
  const [typed, setTyped] = useState("");
  const matches = typed === props.expected;

  function submit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    if (matches && !props.pending) {
      props.onConfirm();
    }
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={(next) => {
        if (!next) {
          setTyped("");
        }
        props.onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{props.title}</DialogTitle>
          <DialogDescription>{props.description}</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
          <div className="flex flex-col gap-2">
            <Label htmlFor={inputId}>
              {props.fieldLabel} <span className="font-mono">{props.expected}</span>
            </Label>
            <Input
              id={inputId}
              value={typed}
              autoComplete="off"
              spellCheck={false}
              onChange={(event) => {
                setTyped(event.target.value);
              }}
              aria-describedby={`${inputId}-hint`}
            />
            <p id={`${inputId}-hint`} className="text-muted-foreground text-xs">
              The delete button stays disabled until the names match.
            </p>
          </div>
          {props.error ? <Alert tone="danger">{props.error}</Alert> : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => { props.onOpenChange(false); }}>
              Cancel
            </Button>
            <Button type="submit" variant="destructive" disabled={!matches || props.pending}>
              {props.pending ? "Working…" : props.confirmLabel}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
