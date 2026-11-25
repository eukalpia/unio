import { useVirtualizer } from "@tanstack/react-virtual";
import { Download, Folder, Trash2 } from "lucide-react";
import { useEffect, useRef } from "react";
import type { ObjectEntry } from "../../lib/api/types";
import { formatBytes, formatDateTime } from "../../lib/format";
import { Button } from "../../components/ui/button";
import { Checkbox } from "../../components/ui/checkbox";
import { displayName } from "./object-paths";
import { cn } from "../../lib/utils";

export type ListRow =
  | { kind: "folder"; key: string }
  | { kind: "object"; key: string; object: ObjectEntry };

const ROW_HEIGHT = 56;
/** Start fetching the next page when the viewport is this many rows from the end. */
const PREFETCH_ROWS = 25;

interface ObjectListProps {
  bucket: string;
  prefix: string;
  rows: readonly ListRow[];
  selected: ReadonlySet<string>;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  onToggle: (key: string) => void;
  onToggleAll: (keys: readonly string[], select: boolean) => void;
  onOpenFolder: (prefix: string) => void;
  onOpenDetails: (object: ObjectEntry) => void;
  onDownload: (key: string) => void;
  onDelete: (keys: readonly string[]) => void;
  onNeedMore: () => void;
}

/**
 * A virtualised table of folders and objects. Only the rows in view are in the
 * DOM, so prefixes with many thousands of keys stay responsive. Pages are
 * requested as the user nears the end of the loaded rows.
 */
export function ObjectList(props: ObjectListProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const { rows, selected, prefix } = props;
  const objectKeys = rows.flatMap((row) => (row.kind === "object" ? [row.key] : []));
  const allSelected = objectKeys.length > 0 && objectKeys.every((key) => selected.has(key));

  // TanStack Virtual returns functions that React Compiler cannot memoise. This list does not memoise them.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 10,
  });
  const items = virtualizer.getVirtualItems();
  const lastVisible = items[items.length - 1]?.index ?? -1;
  const { hasNextPage, isFetchingNextPage, onNeedMore } = props;

  useEffect(() => {
    if (hasNextPage && !isFetchingNextPage && lastVisible >= rows.length - PREFETCH_ROWS) {
      onNeedMore();
    }
  }, [hasNextPage, isFetchingNextPage, lastVisible, rows.length, onNeedMore]);

  return (
    <div
      role="table"
      aria-label={`Objects in ${prefix === "" ? "the bucket root" : prefix}`}
      aria-rowcount={hasNextPage ? -1 : rows.length + 1}
      className="border-border bg-card flex flex-col overflow-hidden rounded-lg border"
    >
      <div role="row" aria-rowindex={1} className="border-border bg-muted/60 text-muted-foreground hidden border-b text-xs font-medium sm:grid sm:grid-cols-[2.5rem_minmax(0,1fr)_7rem_12rem_6.5rem] sm:items-center">
        <div role="columnheader" className="flex items-center justify-center px-2 py-2">
          <Checkbox
            aria-label="Select all objects loaded"
            checked={allSelected}
            disabled={objectKeys.length === 0}
            onChange={(checked) => {
              props.onToggleAll(objectKeys, checked);
            }}
          />
        </div>
        <div role="columnheader" className="px-2 py-2">Name</div>
        <div role="columnheader" className="px-2 py-2 text-right">Size</div>
        <div role="columnheader" className="px-2 py-2">Last modified</div>
        <div role="columnheader" className="px-2 py-2 text-right">
          <span className="sr-only">Actions</span>
        </div>
      </div>

      <div ref={scrollRef} className="relative min-h-0 flex-1 overflow-auto" style={{ height: "min(62dvh, 620px)", minHeight: 240 }}>
        <div className="relative w-full" style={{ height: virtualizer.getTotalSize() }}>
          {items.map((virtualRow) => {
            const row = rows[virtualRow.index];
            if (!row) {
              return null;
            }
            const rowIndex = virtualRow.index + 2;
            return (
              <div
                key={`${row.kind}:${row.key}`}
                role="row"
                aria-rowindex={rowIndex}
                data-index={virtualRow.index}
                className="border-border absolute top-0 left-0 flex w-full items-center border-b text-sm sm:grid sm:grid-cols-[2.5rem_minmax(0,1fr)_7rem_12rem_6.5rem]"
                style={{ height: ROW_HEIGHT, transform: `translateY(${virtualRow.start}px)` }}
              >
                {row.kind === "folder" ? (
                  <FolderRow name={displayName(row.key, prefix)} onOpen={() => { props.onOpenFolder(row.key); }} />
                ) : (
                  <ObjectRow
                    object={row.object}
                    name={displayName(row.key, prefix)}
                    checked={selected.has(row.key)}
                    onToggle={() => { props.onToggle(row.key); }}
                    onOpen={() => { props.onOpenDetails(row.object); }}
                    onDownload={() => { props.onDownload(row.key); }}
                    onDelete={() => { props.onDelete([row.key]); }}
                  />
                )}
              </div>
            );
          })}
        </div>
        {isFetchingNextPage ? (
          <p role="status" className="text-muted-foreground absolute bottom-2 left-1/2 -translate-x-1/2 rounded-md bg-card px-3 py-1 text-xs shadow-sm">
            Loading more objects…
          </p>
        ) : null}
      </div>
    </div>
  );
}

function FolderRow({ name, onOpen }: { name: string; onOpen: () => void }) {
  return (
    <>
      <div role="cell" className="hidden sm:block" />
      <div role="rowheader" className="flex min-w-0 items-center gap-2 px-3 sm:px-2">
        <Folder className="text-muted-foreground size-4 shrink-0" aria-hidden="true" />
        <button
          type="button"
          onClick={onOpen}
          className="text-foreground focus-visible:outline-ring min-w-0 truncate rounded-sm font-medium hover:underline focus-visible:outline-2 focus-visible:outline-offset-2"
        >
          {name}/
        </button>
      </div>
      <div role="cell" className="hidden px-2 text-right text-muted-foreground sm:block">—</div>
      <div role="cell" className="hidden px-2 text-muted-foreground sm:block">Folder</div>
      <div role="cell" className="hidden sm:block" />
    </>
  );
}

function ObjectRow({
  object,
  name,
  checked,
  onToggle,
  onOpen,
  onDownload,
  onDelete,
}: {
  object: ObjectEntry;
  name: string;
  checked: boolean;
  onToggle: () => void;
  onOpen: () => void;
  onDownload: () => void;
  onDelete: () => void;
}) {
  return (
    <>
      <div role="cell" className="flex items-center justify-center px-2 sm:px-2">
        <Checkbox aria-label={`Select ${name}`} checked={checked} onChange={onToggle} />
      </div>
      <div role="rowheader" className="flex min-w-0 flex-1 flex-col px-2 sm:flex-initial sm:px-2">
        <button
          type="button"
          onClick={onOpen}
          className="text-foreground focus-visible:outline-ring block min-w-0 truncate rounded-sm text-left font-medium hover:underline focus-visible:outline-2 focus-visible:outline-offset-2"
        >
          {name}
        </button>
        <span className="text-muted-foreground text-xs sm:hidden">
          {formatBytes(object.size)} · {formatDateTime(object.lastModified)}
        </span>
      </div>
      <div role="cell" className={cn("hidden px-2 text-right tabular-nums sm:block")}>
        {formatBytes(object.size)}
      </div>
      <div role="cell" className="text-muted-foreground hidden truncate px-2 text-sm tabular-nums sm:block">
        {formatDateTime(object.lastModified)}
      </div>
      <div role="cell" className="flex items-center justify-end gap-1 px-2">
        <Button variant="ghost" size="icon-sm" aria-label={`Download ${name}`} onClick={onDownload}>
          <Download aria-hidden="true" />
        </Button>
        <Button variant="ghost" size="icon-sm" aria-label={`Delete ${name}`} onClick={onDelete}>
          <Trash2 aria-hidden="true" />
        </Button>
      </div>
    </>
  );
}
