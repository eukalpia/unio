import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Plus, ShieldCheck, Trash2, UserCheck, UserX } from "lucide-react";
import { useState } from "react";
import { TypeToConfirmDialog } from "../../components/confirm-dialog";
import { PageHeader } from "../../components/chrome";

import { Button } from "../../components/ui/button";
import { Badge, Card } from "../../components/ui/card";
import { Alert, EmptyState, Skeleton } from "../../components/ui/feedback";
import { describeError } from "../../lib/api/errors";
import { api } from "../../lib/api/instance";
import type { UserInfo } from "../../lib/api/types";
import { notify } from "../../lib/notify";
import { queryKeys } from "../../lib/query";
import { CreateUserDialog } from "./create-user-dialog";
import { UserManageSheet } from "./user-manage-sheet";

export function UsersPage() {
  const queryClient = useQueryClient();
  const [creating, setCreating] = useState(false);
  const [managing, setManaging] = useState<string | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [rowError, setRowError] = useState<string | null>(null);

  const users = useQuery({
    queryKey: queryKeys.users,
    queryFn: ({ signal }) => api.listUsers(signal),
  });

  const toggle = useMutation({
    mutationFn: ({ name, enabled }: { name: string; enabled: boolean }) => api.setUserEnabled(name, enabled),
    onSuccess: async (_result, variables) => {
      notify("success", `${variables.enabled ? "Enabled" : "Disabled"} user ${variables.name}.`);
      await queryClient.invalidateQueries({ queryKey: queryKeys.users });
    },
    onError: (error) => {
      setRowError(describeError(error));
    },
  });

  const remove = useMutation({
    mutationFn: (name: string) => api.deleteUser(name),
    onSuccess: async (_result, name) => {
      setDeleting(null);
      setDeleteError(null);
      notify("success", `Deleted user ${name}.`);
      await queryClient.invalidateQueries({ queryKey: queryKeys.users });
    },
    onError: (error) => {
      setDeleteError(describeError(error));
    },
  });

  const list = users.data ?? [];
  const managed: UserInfo | undefined = list.find((user) => user.name === managing);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Users"
        description="People and services that sign in with access keys. Policies decide what each one may do."
        actions={
          <Button
            onClick={() => {
              setCreating(true);
            }}
          >
            <Plus aria-hidden="true" />
            Create user
          </Button>
        }
      />

      {users.isError ? (
        <Alert tone="danger" title="Users could not be loaded">
          <span>{describeError(users.error)}</span>
          <Button variant="link" size="sm" className="mt-1 h-auto p-0" onClick={() => void users.refetch()}>
            Try again
          </Button>
        </Alert>
      ) : null}
      {rowError ? (
        <Alert tone="danger">
          <span>{rowError}</span>
        </Alert>
      ) : null}

      {users.isPending ? (
        <div className="flex flex-col gap-2" aria-busy="true">
          {Array.from({ length: 3 }, (_, index) => (
            <Skeleton key={index} className="h-14" />
          ))}
        </div>
      ) : null}

      {users.data && list.length === 0 ? (
        <EmptyState title="No users yet" description="Create a user, then give it a policy and an access key." />
      ) : null}

      {list.length > 0 ? (
        <Card className="overflow-hidden p-0">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[36rem] text-left text-sm">
              <caption className="sr-only">Users</caption>
              <thead className="bg-muted/60 text-muted-foreground text-xs">
                <tr>
                  <th scope="col" className="px-4 py-2.5 font-medium">Name</th>
                  <th scope="col" className="px-4 py-2.5 font-medium">Status</th>
                  <th scope="col" className="hidden px-4 py-2.5 font-medium md:table-cell">Policies</th>
                  <th scope="col" className="hidden px-4 py-2.5 font-medium sm:table-cell">Access keys</th>
                  <th scope="col" className="px-4 py-2.5 text-right font-medium">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {list.map((user) => (
                  <tr key={user.name} className="border-border border-t">
                    <th scope="row" className="px-4 py-3 text-left font-medium">
                      {user.name}
                    </th>
                    <td className="px-4 py-3">
                      {user.enabled ? (
                        <Badge tone="success">
                          <UserCheck className="size-3" aria-hidden="true" />
                          Enabled
                        </Badge>
                      ) : (
                        <Badge tone="neutral">
                          <UserX className="size-3" aria-hidden="true" />
                          Disabled
                        </Badge>
                      )}
                    </td>
                    <td className="text-muted-foreground hidden px-4 py-3 md:table-cell">
                      {user.policies.length === 0 ? "None" : user.policies.join(", ")}
                    </td>
                    <td className="hidden px-4 py-3 tabular-nums sm:table-cell">{user.accessKeys.length}</td>
                    <td className="px-4 py-3">
                      <div className="flex flex-wrap items-center justify-end gap-1">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => {
                            setManaging(user.name);
                          }}
                          aria-label={`Manage ${user.name}`}
                        >
                          <ShieldCheck aria-hidden="true" />
                          <span>Manage</span>
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={toggle.isPending}
                          aria-label={`${user.enabled ? "Disable" : "Enable"} ${user.name}`}
                          onClick={() => {
                            setRowError(null);
                            toggle.mutate({ name: user.name, enabled: !user.enabled });
                          }}
                        >
                          {user.enabled ? "Disable" : "Enable"}
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon-sm"
                          aria-label={`Delete ${user.name}`}
                          onClick={() => {
                            setDeleteError(null);
                            setDeleting(user.name);
                          }}
                        >
                          <Trash2 aria-hidden="true" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      ) : null}

      <CreateUserDialog open={creating} onOpenChange={setCreating} existing={list.map((user) => user.name)} />

      {managed ? (
        <UserManageSheet
          user={managed}
          onClose={() => {
            setManaging(null);
          }}
        />
      ) : null}

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
          title={`Delete user ${deleting}?`}
          description="The user, its access keys and its policy attachments are removed. Requests signed with its keys stop working."
          expected={deleting}
          fieldLabel="Type the user name to confirm:"
          confirmLabel="Delete user"
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
