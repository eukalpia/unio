/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** "true" in development to run against the in-browser mock server. */
  readonly VITE_MOCKS?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
