import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Database, Plus, Trash2 } from "lucide-react";
import { useId, useMemo, useState } from "react";
import { TypeToConfirmDialog } from "../../components/confirm-dialog";
import { PageHeader } from "../../components/chrome";
import { Button } from "../../components/ui/button";
import { Card } from "../../components/ui/card";
import { Input, Label } from "../../components/ui/form";
import { Alert, EmptyState, Skeleton } from "../../components/ui/feedback";
import { describeError } from "../../lib/api/errors";
import { api } from "../../lib/api/instance";
import type { Bucket } from "../../lib/api/types";
import { formatDateTime } from "../../lib/format";
import { notify } from "../../lib/notify";
import { queryKeys } from "../../lib/query";
import { CreateBucketDialog } from "./create-bucket-dialog";

function versioningLabel(value: string): string {
  return value === "" ? "Never enabled" : value;
}

export function BucketsPage() {
  const queryClient = useQueryClient();
  const filterId = useId();
  const [filter, setFilter] = useState("");
  const [creating, setCreating] = useState(false);
  const [deleting, setDeleting] = useState<Bucket | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const buckets = useQuery({
    queryKey: queryKeys.buckets,
    queryFn: ({ signal }) => api.listBuckets(signal),
  });

  const visible = useMemo(() => {
    const needle = filter.trim().toLowerCase();
    return (buckets.data ?? []).filter((bucket) => needle === "" || bucket.name.includes(needle));
  }, [buckets.data, filter]);

  const remove = useMutation({
    mutationFn: (name: string) => api.deleteBucket(name),
    onSuccess: async (_result, name) => {
      setDeleting(null);
      setDeleteError(null);
      notify("success", `Deleted bucket ${name}.`);
      await queryClient.invalidateQueries({ queryKey: queryKeys.buckets });
    },
    onError: (error) => {
      setDeleteError(describeError(error));
    },
  });


  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Buckets"
        description="Containers for objects. A bucket must be empty before it can be deleted."
        actions={
          <Button
            onClick={() => {
              setCreating(true);
            }}
          >
            <Plus aria-hidden="true" />
            Create bucket
          </Button>
        }
      />

      <div className="flex flex-col gap-2 sm:max-w-sm">
        <Label htmlFor={filterId}>Filter buckets by name</Label>
        <Input
          id={filterId}
          type="search"
          value={filter}
          placeholder="Type part of a name"
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => {
            setFilter(event.target.value);
          }}
        />
      </div>

      {buckets.isError ? (
        <Alert tone="danger" title="Buckets could not be loaded">
          <span>{describeError(buckets.error)}</span>
          <Button variant="link" size="sm" className="mt-1 h-auto p-0" onClick={() => void buckets.refetch()}>
            Try again
          </Button>
        </Alert>
      ) : null}

      {buckets.isPending ? (
        <div className="flex flex-col gap-2" aria-busy="true">
          {Array.from({ length: 4 }, (_, index) => (
            <Skeleton key={index} className="h-12" />
          ))}
        </div>
      ) : null}

      {buckets.data?.length === 0 ? (
        <EmptyState
          title="No buckets yet"
          description="Create a bucket to start storing objects."
          action={
            <Button
              onClick={() => {
                setCreating(true);
              }}
            >
              <Plus aria-hidden="true" />
              Create bucket
            </Button>
          }
        />
      ) : null}

      {buckets.data && buckets.data.length > 0 && visible.length === 0 ? (
        <p role="status" className="text-muted-foreground text-sm">
          No bucket matches “{filter.trim()}”.
        </p>
      ) : null}

      {visible.length > 0 ? (
        <Card className="overflow-hidden p-0">
          <div className="overflow-x-auto">
            <table className="w-full min-w-[32rem] text-left text-sm">
              <caption className="sr-only">Buckets in this cluster</caption>
              <thead className="bg-muted/60 text-muted-foreground text-xs">
                <tr>
                  <th scope="col" className="px-4 py-2.5 font-medium">Name</th>
                  <th scope="col" className="hidden px-4 py-2.5 font-medium md:table-cell">Created</th>
                  <th scope="col" className="px-4 py-2.5 font-medium">Versioning</th>
                  <th scope="col" className="px-4 py-2.5 text-right font-medium">
                    <span className="sr-only">Actions</span>
                  </th>
                </tr>
              </thead>
              <tbody>
                {visible.map((bucket) => (
                  <tr key={bucket.name} className="border-border border-t">
                    <th scope="row" className="px-4 py-3 text-left font-normal">
                      <Link
                        to="/buckets/$bucket"
                        params={{ bucket: bucket.name }}
                        search={{ prefix: "" }}
                        className="text-foreground focus-visible:outline-ring flex items-center gap-2 rounded-sm font-medium hover:underline focus-visible:outline-2 focus-visible:outline-offset-2"
                      >
                        <Database className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
                        {bucket.name}
                      </Link>
                    </th>
                    <td className="text-muted-foreground hidden px-4 py-3 tabular-nums md:table-cell">
                      {formatDateTime(bucket.created)}
                    </td>
                    <td className="px-4 py-3">{versioningLabel(bucket.versioning)}</td>
                    <td className="px-4 py-3 text-right">
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={`Delete bucket ${bucket.name}`}
                        onClick={() => {
                          setDeleteError(null);
                          setDeleting(bucket);
                        }}
                      >
                        <Trash2 aria-hidden="true" />
                        <span className="hidden sm:inline">Delete</span>
                      </Button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </Card>
      ) : null}

      <CreateBucketDialog
        open={creating}
        onOpenChange={setCreating}
        existing={(buckets.data ?? []).map((bucket) => bucket.name)}
      />

      {deleting ? (
        <TypeToConfirmDialog
          key={deleting.name}
          open
          onOpenChange={(open) => {
            if (!open) {
              setDeleting(null);
              setDeleteError(null);
            }
          }}
          title={`Delete bucket ${deleting.name}?`}
          description="The bucket is removed only when it is empty. This cannot be undone."
          expected={deleting.name}
          fieldLabel="Type the bucket name to confirm:"
          confirmLabel="Delete bucket"
          pending={remove.isPending}
          error={deleteError}
          onConfirm={() => {
            remove.mutate(deleting.name);
          }}
        />
      ) : null}
    </div>
  );
}
