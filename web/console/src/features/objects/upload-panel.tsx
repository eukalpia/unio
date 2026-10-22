import { RotateCcw, X } from "lucide-react";
import type { UploadItem, UploadStatus } from "../../lib/upload/queue";
import { Button } from "../../components/ui/button";
import { Card } from "../../components/ui/card";
import { Meter } from "../../components/ui/feedback";
import { uploads } from "./uploads";

const STATUS_TEXT: Record<UploadStatus, string> = {
  queued: "Queued",
  presigning: "Preparing",
  uploading: "Uploading",
  done: "Done",
  failed: "Failed",
  cancelled: "Cancelled",
};

function isActive(status: UploadStatus): boolean {
  return status === "queued" || status === "presigning" || status === "uploading";
}

/** Lists the uploads for this bucket, with progress, cancel and retry. */
export function UploadPanel({ items }: { items: readonly UploadItem[] }) {
  const active = items.filter((item) => isActive(item.status)).length;
  const finished = items.length - active;

  return (
    <Card className="gap-3 p-4" aria-label="Uploads">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p role="status" className="text-sm font-medium">
          {active === 0 ? "No uploads in progress" : `${active} upload${active === 1 ? "" : "s"} in progress, up to 4 at a time`}
        </p>
        {finished > 0 ? (
          <Button variant="ghost" size="sm" onClick={() => { uploads.clearFinished(); }}>
            Clear finished
          </Button>
        ) : null}
      </div>
      <ul className="flex max-h-72 flex-col gap-3 overflow-y-auto">
        {items.map((item) => (
          <li key={item.id} className="flex flex-col gap-1.5 text-sm">
            <div className="flex min-w-0 items-center gap-2">
              <span className="min-w-0 flex-1 truncate font-medium" title={item.key}>
                {item.name}
              </span>
              <span className="text-muted-foreground shrink-0 text-xs">
                {STATUS_TEXT[item.status]}
                {item.status === "uploading" ? ` ${Math.round(item.progress * 100)}%` : ""}
              </span>
              {isActive(item.status) ? (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Cancel upload of ${item.name}`}
                  onClick={() => {
                    uploads.cancel(item.id);
                  }}
                >
                  <X aria-hidden="true" />
                </Button>
              ) : null}
              {item.status === "failed" || item.status === "cancelled" ? (
                <Button
                  variant="ghost"
                  size="icon-sm"
                  aria-label={`Retry upload of ${item.name}`}
                  onClick={() => {
                    uploads.retry(item.id);
                  }}
                >
                  <RotateCcw aria-hidden="true" />
                </Button>
              ) : null}
            </div>
            {item.status === "uploading" || item.status === "done" ? (
              <Meter value={item.progress * 100} max={100} label={`Progress of ${item.name}`} className="h-1.5" />
            ) : null}
            {item.error ? <p className="text-destructive text-xs">{item.error}</p> : null}
          </li>
        ))}
      </ul>
    </Card>
  );
}
