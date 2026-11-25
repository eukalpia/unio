import { describeError, isAbortError } from "../api/errors";

/** Uploads run at most this many at a time. */
export const DEFAULT_UPLOAD_CONCURRENCY = 4;

export type UploadStatus = "queued" | "presigning" | "uploading" | "done" | "failed" | "cancelled";

export interface UploadItem {
  bucket: string;
  id: string;
  name: string;
  key: string;
  size: number;
  status: UploadStatus;
  /** Fraction of the body sent, from 0 to 1. */
  progress: number;
  error: string | null;
}

export interface UploadTransport {
  /** Returns a presigned PUT URL for the object key. */
  presign(bucket: string, key: string, signal: AbortSignal): Promise<string>;
  /** Sends the body to the presigned URL and reports the sent fraction. */
  put(
    url: string,
    body: Blob,
    options: { contentType: string; signal: AbortSignal; onProgress: (fraction: number) => void },
  ): Promise<void>;
}

export interface UploadRequest {
  bucket: string;
  file: File;
  key: string;
}

interface Entry {
  item: UploadItem;
  file: File;
  controller: AbortController | null;
}

type Listener = () => void;

/**
 * A small queue with a concurrency cap. It does not know about React; the UI
 * reads snapshots through useSyncExternalStore.
 */
export class UploadQueue {
  private readonly transport: UploadTransport;
  private readonly concurrency: number;
  private entries: Entry[] = [];
  private snapshot: readonly UploadItem[] = [];
  private readonly listeners = new Set<Listener>();
  private nextId = 1;
  private disposed = false;

  constructor(transport: UploadTransport, concurrency: number = DEFAULT_UPLOAD_CONCURRENCY) {
    if (!Number.isInteger(concurrency) || concurrency < 1) {
      throw new RangeError("upload concurrency must be a positive integer");
    }
    this.transport = transport;
    this.concurrency = concurrency;
  }

  getSnapshot = (): readonly UploadItem[] => this.snapshot;

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  add(requests: readonly UploadRequest[]): void {
    if (this.disposed) {
      return;
    }
    for (const request of requests) {
      const id = `upload-${this.nextId++}`;
      this.entries.push({
        file: request.file,
        controller: null,
        item: {
          bucket: request.bucket,
          id,
          name: request.file.name,
          key: request.key,
          size: request.file.size,
          status: "queued",
          progress: 0,
          error: null,
        },
      });
    }
    this.publish();
    this.pump();
  }

  /** Cancels a queued or running upload. Finished uploads are left as they are. */
  cancel(id: string): void {
    const entry = this.entries.find((candidate) => candidate.item.id === id);
    if (!entry) {
      return;
    }
    if (entry.item.status === "queued") {
      this.update(entry, { status: "cancelled" });
      return;
    }
    if (entry.item.status === "presigning" || entry.item.status === "uploading") {
      entry.controller?.abort();
    }
  }

  /** Queues a failed or cancelled upload again. */
  retry(id: string): void {
    const entry = this.entries.find((candidate) => candidate.item.id === id);
    if (!entry || (entry.item.status !== "failed" && entry.item.status !== "cancelled")) {
      return;
    }
    this.update(entry, { status: "queued", progress: 0, error: null });
    this.pump();
  }

  /** Cancels everything that has not finished and forgets finished items. */
  cancelAll(): void {
    for (const entry of this.entries) {
      if (entry.item.status === "queued") {
        this.update(entry, { status: "cancelled" });
      } else if (entry.item.status === "presigning" || entry.item.status === "uploading") {
        entry.controller?.abort();
      }
    }
  }

  clearFinished(): void {
    this.entries = this.entries.filter(
      (entry) => entry.item.status === "queued" || entry.item.status === "presigning" || entry.item.status === "uploading",
    );
    this.publish();
  }

  dispose(): void {
    this.cancelAll();
    this.disposed = true;
    this.listeners.clear();
  }

  private running(): number {
    return this.entries.filter(
      (entry) => entry.item.status === "presigning" || entry.item.status === "uploading",
    ).length;
  }

  private pump(): void {
    if (this.disposed) {
      return;
    }
    for (const entry of this.entries) {
      if (this.running() >= this.concurrency) {
        return;
      }
      if (entry.item.status === "queued") {
        void this.run(entry);
      }
    }
  }

  private async run(entry: Entry): Promise<void> {
    const controller = new AbortController();
    entry.controller = controller;
    this.update(entry, { status: "presigning", progress: 0, error: null });
    try {
      const url = await this.transport.presign(entry.item.bucket, entry.item.key, controller.signal);
      this.update(entry, { status: "uploading" });
      await this.transport.put(url, entry.file, {
        contentType: entry.file.type || "application/octet-stream",
        signal: controller.signal,
        onProgress: (fraction) => {
          this.update(entry, { progress: Math.min(1, Math.max(0, fraction)) });
        },
      });
      this.update(entry, { status: "done", progress: 1 });
    } catch (error) {
      if (controller.signal.aborted || isAbortError(error)) {
        this.update(entry, { status: "cancelled" });
      } else {
        this.update(entry, { status: "failed", error: describeError(error) });
      }
    } finally {
      entry.controller = null;
      this.pump();
    }
  }

  private update(entry: Entry, patch: Partial<UploadItem>): void {
    entry.item = { ...entry.item, ...patch };
    this.publish();
  }

  private publish(): void {
    this.snapshot = this.entries.map((entry) => entry.item);
    for (const listener of this.listeners) {
      listener();
    }
  }
}
