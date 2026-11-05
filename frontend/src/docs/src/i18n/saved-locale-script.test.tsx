import { describe, expect, it } from "vitest";
import { LOCALE_COOKIE } from "./locales";
import { savedLocaleScript } from "./saved-locale-script";

/** Runs the head script on `url` with `locale` saved, and reports where it went. */
function run(url: string, locale: string) {
  const { pathname, search, hash } = new URL(url);
  let redirect: string | undefined;
  const location = { pathname, search, hash, replace: (to: string) => (redirect = to) };
  const document = { cookie: `${LOCALE_COOKIE}=${locale}`, documentElement: { lang: "en" } };
  new Function("document", "location", savedLocaleScript({ "/installation/": ["ja"] }))(document, location);
  return { redirect, lang: document.documentElement.lang };
}

describe("saved language redirect", () => {
  it("keeps the query and section when going to the translated page", () => {
    expect(run("https://docs.orchestrator.inc/installation/?from=review#where-ao-stores-data", "ja").redirect).toBe(
      "/ja/installation/?from=review#where-ao-stores-data",
    );
  });

  it("stays on the English page when the saved language has no translation", () => {
    expect(run("https://docs.orchestrator.inc/installation/#where-ao-stores-data", "de")).toEqual({
      redirect: undefined,
      lang: "de",
    });
  });
});
