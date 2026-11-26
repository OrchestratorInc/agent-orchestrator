import { defineConfig } from "@lingui/cli";
import { formatter } from "@lingui/format-po";
import { SUPPORTED_LOCALES } from "./src/i18n/locales";

export default defineConfig({
  sourceLocale: "en",
  locales: [...SUPPORTED_LOCALES],
  // Origins follow filesystem order, which differs between machines and
  // would make the catalog diff noisy.
  format: formatter({ origins: false, lineNumbers: false }),
  orderBy: "message",
  compileNamespace: "ts",
  catalogs: [
    {
      path: "<rootDir>/locales/{locale}/messages",
      include: ["<rootDir>/src"],
    },
  ],
});
