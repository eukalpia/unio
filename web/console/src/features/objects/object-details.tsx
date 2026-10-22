import { Download, Trash2 } from "lucide-react";
import type { ObjectEntry } from "../../lib/api/types";
import { formatBytes, formatDateTime } from "../../lib/format";
import { Button } from "../../components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "../../components/ui/dialog";

interface ObjectDetailsProps {
  object: ObjectEntry;
  onClose: () => void;
  onDownload: () => void;
  onDelete: () => void;
}

/** A side drawer with everything the listing knows about one object. */
export function ObjectDetails({ object, onClose, onDownload, onDelete }: ObjectDetailsProps) {
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
          <DialogTitle className="font-mono text-base break-all">{object.key}</DialogTitle>
          <DialogDescription>Object details</DialogDescription>
        </DialogHeader>
        <dl className="grid grid-cols-[7rem_minmax(0,1fr)] gap-x-4 gap-y-3 text-sm">
          <dt className="text-muted-foreground">Size</dt>
          <dd className="tabular-nums">
            {formatBytes(object.size)}
            <span className="text-muted-foreground ml-2 text-xs">({object.size.toLocaleString("en-US")} bytes)</span>
          </dd>
          <dt className="text-muted-foreground">Last modified</dt>
          <dd className="tabular-nums">
            {formatDateTime(object.lastModified)}
            <span className="text-muted-foreground block text-xs">{object.lastModified}</span>
          </dd>
          <dt className="text-muted-foreground">ETag</dt>
          <dd className="font-mono break-all">{object.etag}</dd>
        </dl>
        <DialogFooter>
          <Button variant="outline" onClick={onDownload}>
            <Download aria-hidden="true" />
            Download
          </Button>
          <Button variant="destructive" onClick={onDelete}>
            <Trash2 aria-hidden="true" />
            Delete object
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
