import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Copy, KeyRound, Trash2 } from "lucide-react";
import { useId, useState } from "react";
import { ConfirmDialog } from "../../components/confirm-dialog";
import { Button } from "../../components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../components/ui/dialog";
import { Alert } from "../../components/ui/feedback";
import { Badge, Card } from "../../components/ui/card";
import { Label, NativeSelect } from "../../components/ui/form";
import { describeError, isNotImplemented } from "../../lib/api/errors";
import { api } from "../../lib/api/instance";
import type { CreatedAccessKey, UserInfo } from "../../lib/api/types";
import { formatDateTime } from "../../lib/format";
import { notify } from "../../lib/notify";
import { queryKeys } from "../../lib/query";

interface UserManageSheetProps {
  user: UserInfo;
  onClose: () => void;
}

/**
 * Access keys and policies for one user. A new secret key is shown once in a
 * dialog that closes only by its own button. The secret lives in component
 * state, so it is gone when the dialog closes.
 */
export function UserManageSheet({ user, onClose }: UserManageSheetProps) {
  const queryClient = useQueryClient();
  const policySelectId = useId();
  const policies = useQuery({ queryKey: queryKeys.policies, queryFn: ({ signal }) => api.listPolicies(signal) });
  const [created, setCreated] = useState<CreatedAccessKey | null>(null);
  const [creatingKey, setCreatingKey] = useState(false);
  const [keyError, setKeyError] = useState<string | null>(null);
  const [keyToDelete, setKeyToDelete] = useState<string | null>(null);
  const [chosenPolicy, setChosenPolicy] = useState("");
  const [policyError, setPolicyError] = useState<string | null>(null);

  const refresh = async (): Promise<void> => {
    await queryClient.invalidateQueries({ queryKey: queryKeys.users });
  };

  const attach = useMutation({
    mutationFn: (policy: string) => api.attachPolicy(user.name, policy),
    onSuccess: async (_result, policy) => {
      setChosenPolicy("");
      notify("success", `Attached ${policy} to ${user.name}.`);
      await refresh();
    },
    onError: (error) => {
      setPolicyError(describeError(error));
    },
  });

  const detach = useMutation({
    mutationFn: (policy: string) => api.detachPolicy(user.name, policy),
    onSuccess: async (_result, policy) => {
      notify("success", `Detached ${policy} from ${user.name}.`);
      await refresh();
    },
    onError: (error) => {
      setPolicyError(describeError(error));
    },
  });

  const removeKey = useMutation({
    mutationFn: (id: string) => api.deleteAccessKey(user.name, id),
    onSuccess: async () => {
      setKeyToDelete(null);
      notify("success", "Deleted the access key.");
      await refresh();
    },
    onError: (error) => {
      setKeyError(describeError(error));
      setKeyToDelete(null);
    },
  });

  async function createKey(): Promise<void> {
    setCreatingKey(true);
    setKeyError(null);
    try {
      // Not a mutation, so the secret is never kept in the query cache.
      const result = await api.createAccessKey(user.name);
      setCreated(result);
      await refresh();
    } catch (error) {
      setKeyError(describeError(error));
    } finally {
      setCreatingKey(false);
    }
  }

  const available = (policies.data ?? []).filter((policy) => !user.policies.includes(policy.name));

  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) {
          onClose();
        }
      }}
    >
      <DialogContent variant="sheet">
        <DialogHeader>
          <DialogTitle>{user.name}</DialogTitle>
          <DialogDescription>
            <Badge tone={user.enabled ? "success" : "neutral"}>{user.enabled ? "Enabled" : "Disabled"}</Badge>
          </DialogDescription>
        </DialogHeader>

        <section aria-labelledby="keys-heading" className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-2">
            <h3 id="keys-heading" className="font-display text-base font-semibold">Access keys</h3>
            <Button size="sm" onClick={() => void createKey()} disabled={creatingKey}>
              <KeyRound aria-hidden="true" />
              {creatingKey ? "Creating…" : "Create access key"}
            </Button>
          </div>
          {keyError ? <Alert tone="danger">{keyError}</Alert> : null}
          {user.accessKeys.length === 0 ? (
            <p className="text-muted-foreground text-sm">No access keys. Create one to let this user sign requests.</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {user.accessKeys.map((key) => (
                <li key={key.id} className="border-border flex items-center justify-between gap-3 rounded-md border p-3 text-sm">
                  <div className="flex min-w-0 flex-col">
                    <span className="font-mono text-xs break-all">{key.id}</span>
                    <span className="text-muted-foreground text-xs">Created {formatDateTime(key.created)}</span>
                  </div>
                  <Button
                    variant="ghost"
                    size="icon-sm"
                    aria-label={`Delete access key ${key.id}`}
                    onClick={() => {
                      setKeyError(null);
                      setKeyToDelete(key.id);
                    }}
                  >
                    <Trash2 aria-hidden="true" />
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section aria-labelledby="policies-heading" className="flex flex-col gap-3">
          <h3 id="policies-heading" className="font-display text-base font-semibold">Policies</h3>
          {policyError ? <Alert tone="danger">{policyError}</Alert> : null}
          {user.policies.length === 0 ? (
            <p className="text-muted-foreground text-sm">No policies attached. The user cannot act until one is.</p>
          ) : (
            <ul className="flex flex-col gap-2">
              {user.policies.map((policy) => (
                <li key={policy} className="border-border flex items-center justify-between gap-3 rounded-md border p-3 text-sm">
                  <span className="font-medium">{policy}</span>
                  <Button
                    variant="outline"
                    size="sm"
                    disabled={detach.isPending}
                    aria-label={`Detach ${policy}`}
                    onClick={() => {
                      setPolicyError(null);
                      detach.mutate(policy);
                    }}
                  >
                    Detach
                  </Button>
                </li>
              ))}
            </ul>
          )}

          {isNotImplemented(policies.error) ? (
            <p className="text-muted-foreground text-sm">Attaching policies is not available on this node yet.</p>
          ) : (
          <div className="flex flex-col gap-2">
            <Label htmlFor={policySelectId}>Attach a policy</Label>
            <div className="flex gap-2">
              <NativeSelect
                id={policySelectId}
                value={chosenPolicy}
                disabled={available.length === 0}
                onChange={(event) => {
                  setChosenPolicy(event.target.value);
                }}
                className="min-w-0 flex-1"
              >
                <option value="">{available.length === 0 ? "No more policies to attach" : "Choose a policy"}</option>
                {available.map((policy) => (
                  <option key={policy.name} value={policy.name}>
                    {policy.name}
                  </option>
                ))}
              </NativeSelect>
              <Button
                variant="outline"
                disabled={chosenPolicy === "" || attach.isPending}
                onClick={() => {
                  setPolicyError(null);
                  attach.mutate(chosenPolicy);
                }}
              >
                Attach
              </Button>
            </div>
          </div>
          )}
        </section>

        <Card className="bg-muted/40 p-3 text-xs">
          Passwords are never shown again. A secret key appears once, when it is created.
        </Card>

        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </DialogFooter>
      </DialogContent>

      {created ? (
        <AccessKeySecret
          created={created}
          onDone={() => {
            setCreated(null);
          }}
        />
      ) : null}

      <ConfirmDialog
        open={keyToDelete !== null}
        onOpenChange={(open) => {
          if (!open) {
            setKeyToDelete(null);
          }
        }}
        title="Delete this access key?"
        description="Requests signed with this key stop working straight away."
        confirmLabel="Delete key"
        pending={removeKey.isPending}
        onConfirm={() => {
          if (keyToDelete) {
            removeKey.mutate(keyToDelete);
          }
        }}
      />
    </Dialog>
  );
}

/** Shows a new secret key once. It closes only with its own button, so it is not dismissed by accident. */
function AccessKeySecret({ created, onDone }: { created: CreatedAccessKey; onDone: () => void }) {
  const [copied, setCopied] = useState(false);

  async function copy(): Promise<void> {
    try {
      await navigator.clipboard.writeText(created.secretKey);
      setCopied(true);
      notify("success", "Secret key copied.");
    } catch {
      notify("error", "The secret key could not be copied. Select it and copy it by hand.");
    }
  }

  return (
    <Dialog open>
      <DialogContent
        hideClose
        onEscapeKeyDown={(event) => {
          event.preventDefault();
        }}
        onInteractOutside={(event) => {
          event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>Save this secret key now</DialogTitle>
          <DialogDescription>It is shown only once. Store it in a password manager or a secret store.</DialogDescription>
        </DialogHeader>
        <Alert tone="warning" title="This secret cannot be shown again">
          If you lose it, delete this access key and create a new one.
        </Alert>
        <dl className="flex flex-col gap-3 text-sm">
          <div className="flex flex-col gap-1">
            <dt className="text-muted-foreground">Access key</dt>
            <dd className="font-mono text-xs break-all">{created.accessKeyId}</dd>
          </div>
          <div className="flex flex-col gap-1">
            <dt className="text-muted-foreground">Secret key</dt>
            <dd className="border-border bg-muted rounded-md border p-2 font-mono text-xs break-all select-all">{created.secretKey}</dd>
          </div>
        </dl>
        <DialogFooter>
          <Button variant="outline" onClick={() => void copy()}>
            <Copy aria-hidden="true" />
            {copied ? "Copied" : "Copy secret key"}
          </Button>
          <Button onClick={onDone}>I have saved the secret key</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
