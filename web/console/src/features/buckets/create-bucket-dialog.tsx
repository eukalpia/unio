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
import { bucketNameError } from "../../lib/validation";

interface CreateBucketDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  existing: readonly string[];
}

/**
 * Creates a bucket. The name is checked against the S3 naming rules as the user
 * types, and the server's answer is shown when it refuses a name that passed here.
 */
export function CreateBucketDialog({ open, onOpenChange, existing }: CreateBucketDialogProps) {
  const queryClient = useQueryClient();
  const inputId = useId();
  const errorId = `${inputId}-error`;
  const [name, setName] = useState("");
  const [attempted, setAttempted] = useState(false);
  const [serverError, setServerError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: (bucket: string) => api.createBucket(bucket),
    onSuccess: async (result) => {
      notify("success", `Created bucket ${result.name}.`);
      setName("");
      setAttempted(false);
      setServerError(null);
      onOpenChange(false);
      await queryClient.invalidateQueries({ queryKey: queryKeys.buckets });
    },
    onError: (error) => {
      setServerError(describeError(error));
    },
  });

  const trimmed = name.trim();
  const clientError = trimmed === "" ? null : bucketNameError(trimmed) ?? (existing.includes(trimmed) ? "A bucket with this name already exists." : null);
  const shownError = attempted || trimmed !== "" ? clientError : null;

  function submit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    if (trimmed === "") {
      return;
    }
    if (clientError === null && !create.isPending) {
      setServerError(null);
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
          <DialogTitle>Create a bucket</DialogTitle>
          <DialogDescription>Names are permanent and must be unique across the cluster.</DialogDescription>
        </DialogHeader>
        <form onSubmit={submit} className="flex flex-col gap-4" noValidate>
          <div className="flex flex-col gap-2">
            <Label htmlFor={inputId}>Bucket name</Label>
            <Input
              id={inputId}
              name="bucket"
              value={name}
              autoComplete="off"
              spellCheck={false}
              maxLength={63}
              onChange={(event) => {
                setName(event.target.value);
                setServerError(null);
              }}
              aria-invalid={shownError ? true : undefined}
              aria-describedby={shownError ? errorId : `${inputId}-hint`}
            />
            {shownError ? (
              <FieldError id={errorId} message={shownError} />
            ) : (
              <p id={`${inputId}-hint`} className="text-muted-foreground text-xs">
                3 to 63 lowercase letters, numbers, dots or hyphens. Start and end with a letter or number.
              </p>
            )}
          </div>
          {serverError ? <Alert tone="danger">{serverError}</Alert> : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => { onOpenChange(false); }}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              {create.isPending ? "Creating…" : "Create bucket"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
