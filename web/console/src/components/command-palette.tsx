import { useNavigate } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { useId, useMemo, useState, type KeyboardEvent } from "react";
import { api } from "../lib/api/instance";
import { queryKeys } from "../lib/query";
import { cn } from "../lib/utils";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";
import { Input } from "./ui/form";

interface PaletteItem {
  id: string;
  label: string;
  hint: string;
  to: "/" | "/buckets" | "/users" | "/policies" | "/cluster" | "/buckets/$bucket";
  params?: { bucket: string };
}

const PAGES: readonly PaletteItem[] = [
  { id: "page-dashboard", label: "Dashboard", hint: "Page", to: "/" },
  { id: "page-buckets", label: "Buckets", hint: "Page", to: "/buckets" },
  { id: "page-users", label: "Users", hint: "Page", to: "/users" },
  { id: "page-policies", label: "Policies", hint: "Page", to: "/policies" },
  { id: "page-cluster", label: "Cluster", hint: "Page", to: "/cluster" },
];

/** Filters the pages and buckets by a case-insensitive substring. */
function filterItems(query: string, buckets: readonly string[]): PaletteItem[] {
  const needle = query.trim().toLowerCase();
  const bucketItems: PaletteItem[] = buckets.map((name) => ({
    id: `bucket-${name}`,
    label: name,
    hint: "Bucket",
    to: "/buckets/$bucket",
    params: { bucket: name },
  }));
  return [...PAGES, ...bucketItems].filter((item) => needle === "" || item.label.toLowerCase().includes(needle));
}

export function CommandPalette({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const navigate = useNavigate();
  const listId = useId();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const buckets = useQuery({
    queryKey: queryKeys.buckets,
    queryFn: ({ signal }) => api.listBuckets(signal),
    enabled: open,
  });
  const items = useMemo(
    () => filterItems(query, (buckets.data ?? []).map((bucket) => bucket.name)).slice(0, 50),
    [query, buckets.data],
  );
  const current = items.length === 0 ? -1 : Math.min(active, items.length - 1);

  function choose(item: PaletteItem | undefined): void {
    if (!item) {
      return;
    }
    onOpenChange(false);
    if (item.params) {
      void navigate({ to: "/buckets/$bucket", params: item.params, search: { prefix: "" } });
    } else {
      void navigate({ to: item.to });
    }
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>): void {
    if (event.key === "ArrowDown") {
      event.preventDefault();
      setActive(items.length === 0 ? 0 : (current + 1) % items.length);
    } else if (event.key === "ArrowUp") {
      event.preventDefault();
      setActive(items.length === 0 ? 0 : (current - 1 + items.length) % items.length);
    } else if (event.key === "Home") {
      setActive(0);
    } else if (event.key === "End") {
      setActive(Math.max(0, items.length - 1));
    } else if (event.key === "Enter") {
      event.preventDefault();
      choose(items[current]);
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) {
          setQuery("");
          setActive(0);
        }
        onOpenChange(next);
      }}
    >
      <DialogContent className="top-[20%] max-h-[70dvh] max-w-lg translate-y-0 gap-3 p-4" hideClose>
        <DialogTitle className="sr-only">Jump to a page or bucket</DialogTitle>
        <DialogDescription className="sr-only">
          Type to filter. Use the arrow keys to choose, then press Enter.
        </DialogDescription>
        <Input
          role="combobox"
          aria-expanded={true}
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={current >= 0 ? `${listId}-${current}` : undefined}
          aria-label="Search pages and buckets"
          placeholder="Go to a page or bucket"
          value={query}
          autoComplete="off"
          spellCheck={false}
          onChange={(event) => {
            setQuery(event.target.value);
            setActive(0);
          }}
          onKeyDown={onKeyDown}
          className="h-10"
        />
        <ul id={listId} role="listbox" aria-label="Results" className="flex min-h-0 flex-col gap-0.5 overflow-y-auto">
          {items.map((item, index) => (
            <li
              key={item.id}
              id={`${listId}-${index}`}
              role="option"
              aria-selected={index === current}
              onMouseMove={() => {
                setActive(index);
              }}
              onClick={() => {
                choose(item);
              }}
              className={cn(
                "flex cursor-pointer items-center justify-between gap-3 rounded-md px-3 py-2 text-sm",
                index === current ? "bg-accent text-accent-foreground" : "text-foreground",
              )}
            >
              <span className="truncate font-medium">{item.label}</span>
              <span className="text-muted-foreground shrink-0 text-xs">{item.hint}</span>
            </li>
          ))}
        </ul>
        {items.length === 0 ? (
          <p role="status" className="text-muted-foreground px-3 py-2 text-sm">
            No matching pages or buckets.
          </p>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}
