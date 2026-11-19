import { fileURLToPath } from "node:url";
import { defineConfig } from "vitest/config";

export default defineConfig({
  resolve: {
    alias: {
      "@/": fileURLToPath(new URL("./src/", import.meta.url)),
      // The Lingui macros are compiled by the SWC plugin in the real build.
      // Tests only need the English source text, so a stub stands in.
      "@lingui/core/macro": fileURLToPath(new URL("./test/lingui-macro.ts", import.meta.url)),
    },
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.tsx", "scripts/**/*.test.ts"],
  },
});
