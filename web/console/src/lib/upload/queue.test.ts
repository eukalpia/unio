import { describe, expect, it } from "vitest";
import { UploadQueue, type UploadTransport } from "./queue";

interface Pending {
  key: string;
  resolve: () => void;
  reject: (reason: unknown) => void;
  signal: AbortSignal;
}

/** A transport whose calls wait until the test settles them, so concurrency is observable. */
function controlledTransport() {
  const presigns: Pending[] = [];
  const puts: Pending[] = [];
  let inFlight = 0;
  let peak = 0;
  const track = (promise: Promise<void>) => {
    inFlight += 1;
    peak = Math.max(peak, inFlight);
    return promise.finally(() => {
      inFlight -= 1;
    });
  };
  const transport: UploadTransport = {
    presign: (_bucket, key, signal) =>
      track(
        new Promise<void>((resolve, reject) => {
          presigns.push({ key, signal, resolve, reject });
        }),
      ).then(() => `https://s3.test/${key}`),
    put: (_url, _body, options) =>
      track(
        new Promise<void>((resolve, reject) => {
          puts.push({ key: _url, signal: options.signal, resolve, reject });
          options.onProgress(0.5);
        }),
      ),
  };
  return { transport, presigns, puts, peak: () => peak };
}

function file(name: string, size = 10): File {
  return new File([new Uint8Array(size)], name, { type: "text/plain" });
}

async function flush(): Promise<void> {
  for (let i = 0; i < 5; i += 1) {
    await Promise.resolve();
  }
}

describe("UploadQueue", () => {
  it("never runs more than four uploads at once", async () => {
    const { transport, presigns, puts, peak } = controlledTransport();
    const queue = new UploadQueue(transport, 4);
    const files = Array.from({ length: 10 }, (_, index) => ({ bucket: "b", file: file(`f${index}.txt`), key: `f${index}.txt` }));

    queue.add(files);
    await flush();
    expect(presigns).toHaveLength(4);

    while (presigns.length + puts.length < 20) {
      const next = presigns.find((call) => call.key !== "settled");
      if (next) {
        next.key = "settled";
        next.resolve();
      }
      await flush();
      for (const put of puts) {
        if (put.key !== "settled") {
          put.key = "settled";
          put.resolve();
        }
      }
      await flush();
      if (presigns.every((call) => call.key === "settled") && puts.every((call) => call.key === "settled")) {
        break;
      }
    }

    await flush();
    expect(peak()).toBeLessThanOrEqual(4);
    expect(queue.getSnapshot().every((item) => item.status === "done")).toBe(true);
  });

  it("reports progress while a body is sent", async () => {
    const { transport, presigns, puts } = controlledTransport();
    const queue = new UploadQueue(transport);
    queue.add([{ bucket: "b", file: file("a.txt"), key: "a.txt" }]);
    await flush();
    presigns[0]?.resolve();
    await flush();

    expect(queue.getSnapshot()[0]).toMatchObject({ status: "uploading", progress: 0.5 });
    puts[0]?.resolve();
    await flush();
    expect(queue.getSnapshot()[0]).toMatchObject({ status: "done", progress: 1 });
  });

  it("cancels a queued upload without presigning it", async () => {
    const { transport, presigns } = controlledTransport();
    const queue = new UploadQueue(transport, 1);
    queue.add([
      { bucket: "b", file: file("first.txt"), key: "first.txt" },
      { bucket: "b", file: file("second.txt"), key: "second.txt" },
    ]);
    await flush();
    const second = queue.getSnapshot()[1];

    queue.cancel(second?.id ?? "");

    expect(queue.getSnapshot()[1]?.status).toBe("cancelled");
    expect(presigns.map((call) => call.key)).toEqual(["first.txt"]);
  });

  it("aborts a running upload and marks it cancelled", async () => {
    const { transport, presigns } = controlledTransport();
    const queue = new UploadQueue(transport, 2);
    queue.add([{ bucket: "b", file: file("a.txt"), key: "a.txt" }]);
    await flush();
    const id = queue.getSnapshot()[0]?.id ?? "";

    queue.cancel(id);
    presigns[0]?.reject(new DOMException("aborted", "AbortError"));
    await flush();

    expect(presigns[0]?.signal.aborted).toBe(true);
    expect(queue.getSnapshot()[0]?.status).toBe("cancelled");
  });

  it("records a failure with a safe message and keeps the other uploads going", async () => {
    const { transport, presigns } = controlledTransport();
    const queue = new UploadQueue(transport, 2);
    queue.add([
      { bucket: "b", file: file("bad.txt"), key: "bad.txt" },
      { bucket: "b", file: file("good.txt"), key: "good.txt" },
    ]);
    await flush();

    presigns[0]?.reject(new Error("internal stack detail"));
    presigns[1]?.resolve();
    await flush();

    const [bad, good] = queue.getSnapshot();
    expect(bad).toMatchObject({ status: "failed", error: "Something went wrong. Try again." });
    expect(good?.status).toBe("uploading");
  });

  it("queues a failed upload again on retry", async () => {
    const { transport, presigns } = controlledTransport();
    const queue = new UploadQueue(transport, 2);
    queue.add([{ bucket: "b", file: file("a.txt"), key: "a.txt" }]);
    await flush();
    presigns[0]?.reject(new Error("boom"));
    await flush();
    const id = queue.getSnapshot()[0]?.id ?? "";

    queue.retry(id);
    await flush();

    expect(queue.getSnapshot()[0]?.status).toBe("presigning");
    expect(presigns).toHaveLength(2);
  });

  it("rejects an invalid concurrency", () => {
    const { transport } = controlledTransport();
    expect(() => new UploadQueue(transport, 0)).toThrow(RangeError);
  });
});
