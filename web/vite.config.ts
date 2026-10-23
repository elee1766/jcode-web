import { defineConfig } from "vite";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { VitePWA } from "vite-plugin-pwa";

const api = process.env.JCODE_WEB_API ?? "http://127.0.0.1:7650";

export default defineConfig({
  plugins: [
    svelte(),
    VitePWA({
      strategies: "injectManifest",
      srcDir: "src",
      filename: "sw.ts",
      registerType: "autoUpdate",
      injectRegister: false,
      manifest: {
        name: "jcode web",
        short_name: "jcode",
        description: "Monitor and steer jcode sessions",
        theme_color: "#141a26",
        background_color: "#141a26",
        display: "standalone",
        start_url: "/",
        icons: [
          { src: "/icon.svg", sizes: "any", type: "image/svg+xml", purpose: "any maskable" },
        ],
      },
      injectManifest: { globPatterns: ["**/*.{js,css,html,svg,woff2}"] },
    }),
  ],
  server: {
    proxy: {
      "/jcodeweb.v1.": { target: api, changeOrigin: true },
    },
  },
});
