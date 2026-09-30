/// <reference types="vite/client" />

interface ImportMetaEnv {
  readonly VITE_API_URL?: string;
  readonly VITE_API_PROXY?: string;
  readonly VITE_SHOW_DEMO_BADGE?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
