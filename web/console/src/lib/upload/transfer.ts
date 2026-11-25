import { ApiError } from "../api/errors";

/**
 * Sends a body to a presigned PUT URL with XMLHttpRequest, because fetch cannot
 * report upload progress. Abort cancels the request.
 */
export function putToPresignedUrl(
  url: string,
  body: Blob,
  options: { contentType: string; signal: AbortSignal; onProgress: (fraction: number) => void },
): Promise<void> {
  return new Promise<void>((resolve, reject) => {
    if (options.signal.aborted) {
      reject(new DOMException("The upload was cancelled.", "AbortError"));
      return;
    }
    const xhr = new XMLHttpRequest();
    const onAbort = (): void => {
      xhr.abort();
    };
    xhr.open("PUT", url, true);
    xhr.setRequestHeader("Content-Type", options.contentType);
    xhr.upload.onprogress = (event) => {
      if (event.lengthComputable && event.total > 0) {
        options.onProgress(event.loaded / event.total);
      }
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve();
      } else {
        reject(new ApiError(xhr.status, "UploadFailed", `The upload failed with status ${xhr.status}.`));
      }
    };
    xhr.onerror = () => {
      reject(new ApiError(0, "NetworkError", "Cannot reach the console server."));
    };
    xhr.onabort = () => {
      reject(new DOMException("The upload was cancelled.", "AbortError"));
    };
    xhr.onloadend = () => {
      options.signal.removeEventListener("abort", onAbort);
    };
    options.signal.addEventListener("abort", onAbort, { once: true });
    xhr.send(body);
  });
}
