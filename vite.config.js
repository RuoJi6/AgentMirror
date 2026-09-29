import { defineConfig } from "vite";

const apiTarget =
  process.env.AGENTMIRROR_DEV_API_URL ||
  `http://127.0.0.1:${process.env.AGENTMIRROR_DEV_API_PORT || "8769"}`;

export default defineConfig({
  root: "frontend",
  build: { outDir: "../dist", emptyOutDir: true },
  server: {
    host: "127.0.0.1",
    port: Number(process.env.AGENTMIRROR_DEV_WEB_PORT || 5173),
    strictPort: true,
    proxy: {
      "/api": {
        target: apiTarget,
        changeOrigin: true,
        configure(proxy) {
          proxy.on("proxyReq", (request, incoming) => {
            // The API checks both Host and Origin. Forward same-origin browser
            // writes as same-origin API writes, preserving foreign origins so
            // they continue to fail the backend's existing origin checks.
            if (incoming.headers.origin === `http://${incoming.headers.host}`) {
              request.setHeader("Origin", new URL(apiTarget).origin);
            }
          });
        },
      },
    },
  },
});
