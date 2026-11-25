import { useSyncExternalStore } from "react";
import { api } from "../../lib/api/instance";
import { queryClient } from "../../lib/query";
import { UploadQueue, type UploadItem } from "../../lib/upload/queue";
import { putToPresignedUrl } from "../../lib/upload/transfer";

/** Presigned upload URLs stay valid for 15 minutes, which covers a slow large upload. */
export const UPLOAD_URL_SECONDS = 900;

/**
 * One queue for the whole console, so the four-upload cap holds across buckets.
 * It lives outside React, so uploads keep their state when the object list
 * unmounts and comes back.
 */
export const uploads = new UploadQueue({
  presign: async (bucket, key, signal) => {
    const { url } = await api.presign(bucket, { method: "PUT", key, expiresSeconds: UPLOAD_URL_SECONDS }, signal);
    return url;
  },
  put: async (url, body, options) => {
    await putToPresignedUrl(url, body, options);
    void queryClient.invalidateQueries({ queryKey: ["objects"] });
  },
});

export function useUploadItems(): readonly UploadItem[] {
  return useSyncExternalStore(uploads.subscribe, uploads.getSnapshot, uploads.getSnapshot);
}
