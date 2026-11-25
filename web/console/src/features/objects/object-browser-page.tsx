import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams, useSearch } from "@tanstack/react-router";
import { ChevronRight, Home, RefreshCw, Trash2, Upload } from "lucide-react";
import { useMemo, useRef, useState, type DragEvent } from "react";
import { ConfirmDialog } from "../../components/confirm-dialog";
import { PageHeader } from "../../components/chrome";
import { Alert } from "../../components/ui/feedback";
import { Button } from "../../components/ui/button";
import { EmptyState, Skeleton } from "../../components/ui/feedback";
import { describeError } from "../../lib/api/errors";
import { api } from "../../lib/api/instance";
import type { ObjectEntry } from "../../lib/api/types";
import { notify } from "../../lib/notify";
import { queryKeys } from "../../lib/query";
import { mapWithConcurrency } from "../../lib/concurrency";
import { ObjectDetails } from "./object-details";
import { ObjectList, type ListRow } from "./object-list";
import { displayName, normalisePrefix, prefixCrumbs, uniquePrefixes, uploadKey } from "./object-paths";
import { UploadPanel } from "./upload-panel";
import { uploads, useUploadItems } from "./uploads";
import { cn } from "../../lib/utils";

const PAGE_SIZE = 200;
const DELETE_CONCURRENCY = 4;
/** The object key limit in the S3 API, in bytes. */
const MAX_KEY_BYTES = 1024;
const DOWNLOAD_URL_SECONDS = 300;
const EMPTY_SET: ReadonlySet<string> = new Set<string>();

interface PendingDelete {
  scope: string;
  keys: readonly string[];
}

export function ObjectBrowserPage() {
  const { bucket } = useParams({ from: "/app/buckets/$bucket" });
  const { prefix: rawPrefix } = useSearch({ from: "/app/buckets/$bucket" });
  const prefix = normalisePrefix(rawPrefix);
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const scope = `${bucket}\u0000${prefix}`;

  // Selection, details and the pending delete are tied to one location, so they reset when the prefix changes.
  const [selection, setSelection] = useState<{ scope: string; keys: ReadonlySet<string> }>({ scope, keys: EMPTY_SET });
  const selected = selection.scope === scope ? selection.keys : EMPTY_SET;
  const [details, setDetails] = useState<{ scope: string; object: ObjectEntry } | null>(null);
  const [pendingDelete, setPendingDelete] = useState<PendingDelete | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0);
  const fileInput = useRef<HTMLInputElement>(null);
  const uploadItems = useUploadItems();
  const bucketUploads = useMemo(() => uploadItems.filter((item) => item.bucket === bucket), [uploadItems, bucket]);

  const listing = useInfiniteQuery({
    queryKey: queryKeys.objects(bucket, prefix),
    queryFn: ({ pageParam, signal }) =>
      api.listObjects(bucket, { prefix, delimiter: "/", token: pageParam === "" ? undefined : pageParam, max: PAGE_SIZE }, signal),
    initialPageParam: "",
    getNextPageParam: (page) => (page.nextToken === "" ? undefined : (page.nextToken ?? undefined)),
  });

  const rows = useMemo<ListRow[]>(() => {
    const pages = listing.data?.pages ?? [];
    const folders: ListRow[] = uniquePrefixes(pages.map((page) => page.prefixes)).map((key) => ({ kind: "folder", key }));
    const seen = new Set<string>();
    const objects: ListRow[] = [];
    for (const page of pages) {
      for (const object of page.objects) {
        if (!seen.has(object.key)) {
          seen.add(object.key);
          objects.push({ kind: "object", key: object.key, object });
        }
      }
    }
    return [...folders, ...objects];
  }, [listing.data]);

  const crumbs = prefixCrumbs(prefix);

  function go(next: string): void {
    void navigate({ to: "/buckets/$bucket", params: { bucket }, search: { prefix: next } });
  }

  function toggle(key: string): void {
    setSelection((current) => {
      const base = current.scope === scope ? current.keys : EMPTY_SET;
      const next = new Set(base);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return { scope, keys: next };
    });
  }

  function toggleAll(keys: readonly string[], select: boolean): void {
    setSelection(() => ({ scope, keys: select ? new Set(keys) : new Set<string>() }));
  }

  function enqueue(files: readonly File[]): void {
    const requests: { bucket: string; file: File; key: string }[] = [];
    const tooLong: string[] = [];
    for (const file of files) {
      const key = uploadKey(prefix, file.name);
      if (new TextEncoder().encode(key).byteLength > MAX_KEY_BYTES) {
        tooLong.push(file.name);
      } else {
        requests.push({ bucket, file, key });
      }
    }
    if (requests.length > 0) {
      uploads.add(requests);
    }
    if (tooLong.length > 0) {
      notify("error", `Skipped ${tooLong.length} file${tooLong.length === 1 ? "" : "s"} whose names are too long for an object key.`);
    }
  }

  function onDragEnter(event: DragEvent<HTMLElement>): void {
    if (!event.dataTransfer.types.includes("Files")) {
      return;
    }
    event.preventDefault();
    dragDepth.current += 1;
    setDragging(true);
  }

  function onDragLeave(event: DragEvent<HTMLElement>): void {
    if (!event.dataTransfer.types.includes("Files")) {
      return;
    }
    dragDepth.current = Math.max(0, dragDepth.current - 1);
    if (dragDepth.current === 0) {
      setDragging(false);
    }
  }

  function onDrop(event: DragEvent<HTMLElement>): void {
    if (!event.dataTransfer.types.includes("Files")) {
      return;
    }
    event.preventDefault();
    dragDepth.current = 0;
    setDragging(false);
    enqueue(Array.from(event.dataTransfer.files));
  }

  async function download(key: string): Promise<void> {
    try {
      const { url } = await api.presign(bucket, { method: "GET", key, expiresSeconds: DOWNLOAD_URL_SECONDS });
      const link = document.createElement("a");
      link.href = url;
      link.rel = "noopener";
      link.download = displayName(key, prefix);
      link.click();
    } catch (error) {
      notify("error", describeError(error));
    }
  }

  async function runDelete(): Promise<void> {
    if (pendingDelete?.scope !== scope) {
      return;
    }
    const keys = pendingDelete.keys;
    setDeleting(true);
    setDeleteError(null);
    const results = await mapWithConcurrency(keys, DELETE_CONCURRENCY, (key) => api.deleteObject(bucket, key));
    setDeleting(false);
    const failures = results.flatMap((result) => (result.status === "rejected" ? [describeError(result.reason)] : []));
    await queryClient.invalidateQueries({ queryKey: queryKeys.objects(bucket, prefix) });
    if (failures.length === 0) {
      notify("success", `Deleted ${keys.length} object${keys.length === 1 ? "" : "s"}.`);
      setPendingDelete(null);
      setSelection({ scope, keys: EMPTY_SET });
      setDetails(null);
    } else if (failures.length === keys.length) {
      setDeleteError(failures[0] ?? "The objects could not be deleted.");
    } else {
      notify("error", `${failures.length} of ${keys.length} objects could not be deleted.`);
      setPendingDelete(null);
    }
  }

  const isEmpty = !listing.isPending && !listing.isError && rows.length === 0;
  const selectedCount = selected.size;
  const detailsObject = details?.scope === scope ? details.object : null;

  return (
    <div className="flex min-w-0 flex-col gap-5">
      <PageHeader
        title={bucket}
        description={
          <span>
            {listing.isPending ? "Loading objects" : `${rows.length} item${rows.length === 1 ? "" : "s"} loaded${listing.hasNextPage ? ", more available" : ""}`}
          </span>
        }
        actions={
          <>
            <input
              ref={fileInput}
              type="file"
              multiple
              className="hidden"
              aria-label="Choose files to upload"
              onChange={(event) => {
                enqueue(Array.from(event.target.files ?? []));
                event.target.value = "";
              }}
            />
            <Button
              variant="outline"
              onClick={() => {
                fileInput.current?.click();
              }}
            >
              <Upload aria-hidden="true" />
              Upload files
            </Button>
            <Button
              variant="destructive"
              disabled={selectedCount === 0}
              onClick={() => {
                setDeleteError(null);
                setPendingDelete({ scope, keys: [...selected] });
              }}
            >
              <Trash2 aria-hidden="true" />
              Delete {selectedCount > 0 ? selectedCount : ""} selected
            </Button>
            <Button
              variant="ghost"
              size="icon"
              aria-label="Refresh the listing"
              onClick={() => {
                void listing.refetch();
              }}
            >
              <RefreshCw className={cn(listing.isFetching && "animate-spin")} aria-hidden="true" />
            </Button>
          </>
        }
      />

      <nav aria-label="Breadcrumb" className="-mx-1 flex min-w-0 items-center gap-1 overflow-x-auto px-1 text-sm">
        <ol className="flex min-w-0 items-center gap-1 whitespace-nowrap">
          <li>
            <button
              type="button"
              onClick={() => {
                go("");
              }}
              aria-current={prefix === "" ? "page" : undefined}
              className="text-muted-foreground hover:text-foreground focus-visible:outline-ring flex items-center gap-1 rounded-sm px-1 py-0.5 focus-visible:outline-2 focus-visible:outline-offset-2"
            >
              <Home className="size-3.5" aria-hidden="true" />
              <span>{bucket}</span>
              <span className="sr-only">root</span>
            </button>
          </li>
          {crumbs.map((crumb, index) => {
            const last = index === crumbs.length - 1;
            return (
              <li key={crumb.prefix} className="flex items-center gap-1">
                <ChevronRight className="text-muted-foreground size-3.5" aria-hidden="true" />
                <button
                  type="button"
                  onClick={() => {
                    go(crumb.prefix);
                  }}
                  aria-current={last ? "page" : undefined}
                  className={cn(
                    "focus-visible:outline-ring rounded-sm px-1 py-0.5 focus-visible:outline-2 focus-visible:outline-offset-2",
                    last ? "text-foreground font-medium" : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {crumb.label}
                </button>
              </li>
            );
          })}
        </ol>
      </nav>

      {bucketUploads.length > 0 ? <UploadPanel items={bucketUploads} /> : null}

      <section
        aria-label="Object listing and upload target"
        onDragEnter={onDragEnter}
        onDragOver={(event) => {
          if (event.dataTransfer.types.includes("Files")) {
            event.preventDefault();
          }
        }}
        onDragLeave={onDragLeave}
        onDrop={onDrop}
        className={cn("relative flex min-w-0 flex-col gap-3 rounded-lg transition-shadow", dragging && "ring-primary ring-2 ring-offset-2 ring-offset-background")}
      >
        {dragging ? (
          <div className="bg-card/80 pointer-events-none absolute inset-0 z-10 flex items-center justify-center rounded-lg backdrop-blur-[1px]">
            <p className="font-medium">Drop files to upload to {prefix === "" ? "the bucket root" : prefix}</p>
          </div>
        ) : null}

        {listing.isError ? (
          <Alert tone="danger" title="The listing could not be loaded">
            <span>{describeError(listing.error)}</span>
            <Button
              variant="link"
              size="sm"
              className="mt-1 h-auto p-0"
              onClick={() => {
                void listing.refetch();
              }}
            >
              Try again
            </Button>
          </Alert>
        ) : null}

        {listing.isPending ? (
          <div className="flex flex-col gap-2" aria-busy="true">
            {Array.from({ length: 6 }, (_, index) => (
              <Skeleton key={index} className="h-12" />
            ))}
          </div>
        ) : null}

        {isEmpty ? (
          <EmptyState
            title="Nothing here yet"
            description="Drop files anywhere in this area, or use Upload files. Each file becomes an object under this prefix."
            action={
              <Button
                variant="outline"
                onClick={() => {
                  fileInput.current?.click();
                }}
              >
                <Upload aria-hidden="true" />
                Upload files
              </Button>
            }
          />
        ) : null}

        {!listing.isPending && !listing.isError && rows.length > 0 ? (
          <ObjectList
            bucket={bucket}
            prefix={prefix}
            rows={rows}
            selected={selected}
            hasNextPage={listing.hasNextPage}
            isFetchingNextPage={listing.isFetchingNextPage}
            onToggle={toggle}
            onToggleAll={toggleAll}
            onOpenFolder={go}
            onOpenDetails={(object) => {
              setDetails({ scope, object });
            }}
            onDownload={(key) => {
              void download(key);
            }}
            onDelete={(keys) => {
              setDeleteError(null);
              setPendingDelete({ scope, keys });
            }}
            onNeedMore={() => {
              void listing.fetchNextPage();
            }}
          />
        ) : null}
      </section>

      {detailsObject ? (
        <ObjectDetails
          object={detailsObject}
          onClose={() => {
            setDetails(null);
          }}
          onDownload={() => {
            void download(detailsObject.key);
          }}
          onDelete={() => {
            setDetails(null);
            setDeleteError(null);
            setPendingDelete({ scope, keys: [detailsObject.key] });
          }}
        />
      ) : null}

      <ConfirmDialog
        open={pendingDelete !== null && pendingDelete.scope === scope}
        onOpenChange={(open) => {
          if (!open) {
            setPendingDelete(null);
            setDeleteError(null);
          }
        }}
        title={`Delete ${pendingDelete?.keys.length ?? 0} object${(pendingDelete?.keys.length ?? 0) === 1 ? "" : "s"}?`}
        description="Deleted objects cannot be recovered. Each object is removed in its own request, four at a time."
        confirmLabel="Delete objects"
        pending={deleting}
        error={deleteError}
        onConfirm={() => {
          void runDelete();
        }}
      />
    </div>
  );
}
