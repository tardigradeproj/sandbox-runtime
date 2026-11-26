import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The backend's console listener (internal/cfg.Console.ListenAddress,
// default 127.0.0.1:9090) serves the three Connect services at
// /<package>.<Service>/* -- see pkg/api/runtime/v1/api.proto's
// `package runtime.v1;`. Proxying them here keeps dev same-origin
// (the browser only ever talks to Vite's own port), so no CORS
// configuration is needed anywhere, in dev or in the production
// single-binary build.
const BACKEND = "http://127.0.0.1:9090";

export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/runtime.v1.RuntimeService": BACKEND,
      "/runtime.v1.RootFSService": BACKEND,
      "/runtime.v1.NetworkService": BACKEND,
    },
  },
});
