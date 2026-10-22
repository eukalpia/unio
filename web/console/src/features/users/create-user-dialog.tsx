import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useId, useState, type SyntheticEvent } from "react";
import { Button } from "../../components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../components/ui/dialog";
import { Alert } from "../../components/ui/feedback";
import { FieldError, Input, Label } from "../../components/ui/form";
import { describeError } from "../../lib/api/errors";
import { api } from "../../lib/api/instance";
import { notify } from "../../lib/notify";
import { queryKeys } from "../../lib/query";
import { userNameError } from "../../lib/validation";

interface CreateUserDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existing: readonly string[];
}

export function CreateUserDialog({ open, onOpenChange, existing }: CreateUserDialogProps) {
  const queryClient = useQueryClient();
  const inputId = useId();
  const errorId = `${inputId}-error`;
  const [name, setName] = useState("");
  const [attempted, setAttempted] = useState(false);
  const [serverError, setServerError] = useState<string | null>(null);
  const trimmed = name.trim();
  const clientError =
    trimmed === "" ? null : (userNameError(trimmed) ?? (existing.includes(trimmed) ? "A user with this name already exists." : null));
  const shownError = attempted || trimmed !== "" ? clientError : null;

  const create = useMutation({
    mutationFn: (user: string) => api.createUser(user),
    onSuccess: async (_result, user) => {
      notify("success", `Created user ${user}. Add a policy and an access key next.`);
      setName("");
      setAttempted(false);
      setServerError(null);
      onOpenChange(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.users });
    },
    onError: (error) => {
      setServerError(describeError(error));
    },
  });

  function submit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    if (trimmed !== "" && clientError === null && !create.isPending) {
      create.mutate(trimmed);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setName("");
          setAttempted(false);
          setServerError(null);
        }
        onOpenChange(next);
      }}
    >
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Create a user</DialogTitle>
          <DialogDescription>A user has no access until it has a policy and an access key.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
          <div className="flex flex-col gap-2">
            <Label htmlFor={inputId}>User name</Label>
            <Input
              id={inputId}
              value={name}
              autoComplete="off"
              spellCheck={false}
              maxLength={64}
              onChange={(event) => {
                setName(event.target.value);
                setServerError(null);
              }}
              aria-invalid={shownError ? true : undefined}
              aria-describedby={shownError ? errorId : undefined}
            />
            <FieldError id={errorId} message={shownError} />
          </div>
          {serverError ? <Alert tone="danger">{serverError}</Alert> : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => { onOpenChange(false); }}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending ? "Creating…" : "Create user"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
