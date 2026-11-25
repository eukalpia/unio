import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, ShieldCheck } from "lucide-react";
import { useId, useMemo, useState, type SyntheticEvent } from "react";
import { TypeToConfirmDialog } from "../../components/confirm-dialog";
import { PageHeader } from "../../components/chrome";
import { Button } from "../../components/ui/button";
import { Card, CardDescription, CardHeader, CardTitle } from "../../components/ui/card";
import { FieldError, Input, Label, Textarea } from "../../components/ui/form";
import { Alert, EmptyState, Skeleton } from "../../components/ui/feedback";
import { describeError, isNotImplemented } from "../../lib/api/errors";
import { api } from "../../lib/api/instance";
import type { PolicyInfo } from "../../lib/api/types";
import { notify } from "../../lib/notify";
import { queryKeys } from "../../lib/query";
import { cn } from "../../lib/utils";
import { parsePolicyText, policyNameError } from "../../lib/validation";

type Selection = { mode: "new" } | { mode: "edit"; name: string } | null;

export function PoliciesPage() {
  const queryClient = useQueryClient();
  const [selection, setSelection] = useState<Selection>(null);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const policies = useQuery({
    queryKey: queryKeys.policies,
    queryFn: ({ signal }) => api.listPolicies(signal),
  });

  const list = useMemo(() => policies.data ?? [], [policies.data]);
  // The node answers 501 until the IAM change for named policies lands.
  const unavailable = isNotImplemented(policies.error);
  const current: PolicyInfo | undefined =
    selection?.mode === "edit" ? list.find((policy) => policy.name === selection.name) : undefined;

  const remove = useMutation({
    mutationFn: (name: string) => api.deletePolicy(name),
    onSuccess: async (_result, name) => {
      setDeleting(null);
      setDeleteError(null);
      setSelection(null);
      notify("success", `Deleted policy ${name}.`);
      await queryClient.invalidateQueries({ queryKey: queryKeys.policies });
    },
    onError: (error) => {
      setDeleteError(describeError(error));
    },
  });

  const editorKey = selection === null ? "none" : selection.mode === "new" ? "new" : `edit:${selection.name}`;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Policies"
        description="JSON documents that grant or deny actions on buckets and objects. Attach them to users."
        actions={
          <Button
            disabled={unavailable}
            onClick={() => {
              setSelection({ mode: "new" });
            }}
          >
            <Plus aria-hidden="true" />
            New policy
          </Button>
        }
      />

      {policies.isError && !unavailable ? (
        <Alert tone="danger" title="Policies could not be loaded">
          <span>{describeError(policies.error)}</span>
          <Button variant="link" size="sm" className="mt-1 h-auto p-0" onClick={() => void policies.refetch()}>
            Try again
          </Button>
        </Alert>
      ) : null}

      {unavailable ? (
        <Card className="gap-2">
          <CardTitle>Named policies are not available on this node yet</CardTitle>
          <CardDescription>
            The policy API answers 501 Not Implemented until the IAM change lands. Users keep the policy names they already have, and those names still appear on the Users page.
          </CardDescription>
        </Card>
      ) : (
      <div className="grid gap-4 lg:grid-cols-[18rem_minmax(0,1fr)]">
        <Card className="gap-1 p-2" aria-label="Policy list">
          {policies.isPending ? (
            <div className="flex flex-col gap-2 p-2" aria-busy="true">
              {Array.from({ length: 3 }, (_, index) => (
                <Skeleton key={index} className="h-9" />
              ))}
            </div>
          ) : null}
          {policies.data && list.length === 0 ? (
            <p className="text-muted-foreground p-3 text-sm">No policies yet. Create one to get started.</p>
          ) : null}
          <ul className="flex flex-col gap-0.5">
            {list.map((policy) => {
              const active = selection?.mode === "edit" && selection.name === policy.name;
              return (
                <li key={policy.name}>
                  <button
                    type="button"
                    aria-current={active ? "true" : undefined}
                    onClick={() => {
                      setSelection({ mode: "edit", name: policy.name });
                    }}
                    className={cn(
                      "focus-visible:outline-ring flex w-full items-center gap-2 rounded-md px-3 py-2 text-left text-sm font-medium focus-visible:outline-2 focus-visible:outline-offset-2",
                      active ? "bg-accent text-foreground" : "text-muted-foreground hover:bg-accent hover:text-foreground",
                    )}
                  >
                    <ShieldCheck className="size-4 shrink-0" aria-hidden="true" />
                    <span className="truncate">{policy.name}</span>
                  </button>
                </li>
              );
            })}
          </ul>
        </Card>

        {selection === null ? (
          <EmptyState title="Choose a policy" description="Select a policy to read or edit it, or create a new one." />
        ) : (
          <PolicyEditor
            key={editorKey}
            initial={current}
            existing={list.map((policy) => policy.name)}
            onSaved={(name) => {
              setSelection({ mode: "edit", name });
            }}
            onDelete={(name) => {
              setDeleteError(null);
              setDeleting(name);
            }}
          />
        )}
      </div>
      )}

      {deleting ? (
        <TypeToConfirmDialog
          key={deleting}
          open
          onOpenChange={(open) => {
            if (!open) {
              setDeleting(null);
              setDeleteError(null);
            }
          }}
          title={`Delete policy ${deleting}?`}
          description="A policy still attached to a user cannot be deleted. Detach it first."
          expected={deleting}
          fieldLabel="Type the policy name to confirm:"
          confirmLabel="Delete policy"
          pending={remove.isPending}
          error={deleteError}
          onConfirm={() => {
            remove.mutate(deleting);
          }}
        />
      ) : null}
    </div>
  );
}

function PolicyEditor({
  initial,
  existing,
  onSaved,
  onDelete,
}: {
  initial: PolicyInfo | undefined;
  existing: readonly string[];
  onSaved: (name: string) => void;
  onDelete: (name: string) => void;
}) {
  const queryClient = useQueryClient();
  const nameId = useId();
  const bodyId = useId();
  const errorId = `${bodyId}-error`;
  const isNew = initial === undefined;
  const [name, setName] = useState(initial?.name ?? "");
  const [text, setText] = useState(() => (initial ? JSON.stringify(initial.document, null, 2) : DEFAULT_DOCUMENT));
  const [attempted, setAttempted] = useState(false);
  const [serverError, setServerError] = useState<string | null>(null);

  const trimmedName = name.trim();
  const nameError = isNew
    ? trimmedName === ""
      ? attempted
        ? "Enter a policy name."
        : null
      : (policyNameError(trimmedName) ?? (existing.includes(trimmedName) ? "A policy with this name already exists." : null))
    : null;
  const parsed = useMemo(() => parsePolicyText(text), [text]);
  const bodyError = parsed.ok ? null : parsed.message;

  const save = useMutation({
    mutationFn: ({ policy, document }: { policy: string; document: Record<string, unknown> }) =>
      api.putPolicy(policy, document),
    onSuccess: async (_result, variables) => {
      setServerError(null);
      notify("success", `Saved policy ${variables.policy}.`);
      await queryClient.invalidateQueries({ queryKey: queryKeys.policies });
      onSaved(variables.policy);
    },
    onError: (error) => {
      setServerError(describeError(error));
    },
  });

  function submit(event: SyntheticEvent<HTMLFormElement>): void {
    event.preventDefault();
    setAttempted(true);
    setServerError(null);
    if (nameError || bodyError || !parsed.ok || save.isPending) {
      return;
    }
    if (trimmedName === "" || (isNew && nameError)) {
      return;
    }
    save.mutate({ policy: trimmedName, document: parsed.document });
  }

  function format(): void {
    if (parsed.ok) {
      setText(parsed.pretty);
    }
  }

  return (
    <Card className="min-w-0">
      <CardHeader>
        <CardTitle>{isNew ? "New policy" : `Policy ${initial.name}`}</CardTitle>
        <CardDescription>
          The document is checked as JSON here. The node checks the policy language when you save.
        </CardDescription>
      </CardHeader>
      <form onSubmit={submit} className="flex min-w-0 flex-col gap-4" noValidate>
        {isNew ? (
          <div className="flex flex-col gap-2">
            <Label htmlFor={nameId}>Policy name</Label>
            <Input
              id={nameId}
              value={name}
              autoComplete="off"
              spellCheck={false}
              maxLength={128}
              onChange={(event) => {
                setName(event.target.value);
                setServerError(null);
              }}
              aria-invalid={nameError ? true : undefined}
              aria-describedby={nameError ? `${nameId}-error` : undefined}
            />
            <FieldError id={`${nameId}-error`} message={nameError} />
          </div>
        ) : null}

        <div className="flex flex-col gap-2">
          <Label htmlFor={bodyId}>Policy document (JSON)</Label>
          <Textarea
            id={bodyId}
            value={text}
            rows={16}
            spellCheck={false}
            autoCapitalize="off"
            autoCorrect="off"
            className="font-mono text-[13px] leading-relaxed"
            onChange={(event) => {
              setText(event.target.value);
              setServerError(null);
            }}
            aria-invalid={attempted && bodyError ? true : undefined}
            aria-describedby={bodyError ? errorId : undefined}
          />
          {bodyError ? (
            <FieldError id={errorId} message={bodyError} />
          ) : (
            <p className="text-success text-xs">Valid JSON object.</p>
          )}
        </div>

        {serverError ? <Alert tone="danger">{serverError}</Alert> : null}

        <div className="flex flex-col-reverse gap-2 sm:flex-row sm:flex-wrap sm:items-center sm:justify-between">
          <div className="flex flex-wrap gap-2">
            <Button type="submit" disabled={save.isPending}>
              {save.isPending ? "Saving…" : "Save policy"}
            </Button>
            <Button type="button" variant="outline" onClick={format} disabled={!parsed.ok}>
              Format JSON
            </Button>
          </div>
          {isNew ? null : (
            <Button
              type="button"
              variant="ghost"
              className="text-destructive hover:text-destructive"
              onClick={() => {
                onDelete(initial.name);
              }}
            >
              Delete policy
            </Button>
          )}
        </div>
      </form>
    </Card>
  );
}

const DEFAULT_DOCUMENT = `{
  "Version": "2026-01-01",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["s3:GetObject"],
      "Resource": ["arn:unio:s3:::bucket-name/*"]
    }
  ]
}`;
