import { describe, expect, it } from "vitest";
import { LOCALE_COOKIE, type SupportedLocale } from "./locales";
import { savedLocaleScript } from "./saved-locale-script";

const SERVED: SupportedLocale[] = ["en", "ja", "zh-CN"];

/** Runs the head script on `url` with `cookie` sent, and reports where it went. */
function runWithCookie(url: string, cookie: string) {
  const { pathname, search, hash } = new URL(url);
  let redirect: string | undefined;
  const location = { pathname, search, hash, replace: (to: string) => (redirect = to) };
  const document = { cookie, documentElement: { lang: "en" } };
  new Function("document", "location", savedLocaleScript({ "/installation/": ["ja", "zh-CN"] }, SERVED))(document, location);
  return { redirect, lang: document.documentElement.lang };
}

const run = (url: string, locale: string) => runWithCookie(url, `${LOCALE_COOKIE}=${locale}`);

describe("saved language redirect", () => {
  it("keeps the query and section when going to the translated page", () => {
    expect(run("https://docs.orchestrator.inc/installation/?from=review#where-ao-stores-data", "ja").redirect).toBe(
      "/ja/installation/?from=review#where-ao-stores-data",
    );
    expect(run("https://docs.orchestrator.inc/installation/", "zh-CN").redirect).toBe("/zh-CN/installation/");
  });

  it("stays on the English page in the saved language when the page has no translation", () => {
    expect(run("https://docs.orchestrator.inc/cli/", "ja")).toEqual({ redirect: undefined, lang: "ja" });
  });

  it.each(["fr", "de", "ko", "xx", "JA", ""])("leaves the page in English for a language the docs do not serve: %j", (locale) => {
    expect(run("https://docs.orchestrator.inc/installation/", locale)).toEqual({ redirect: undefined, lang: "en" });
  });

  it("follows a served language when the landing's unserved one is also sent", () => {
    expect(runWithCookie("https://docs.orchestrator.inc/installation/", "ao_locale=fr; ao_locale=ja").redirect).toBe(
      "/ja/installation/",
    );
  });
});
