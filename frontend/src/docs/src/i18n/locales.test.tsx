import { describe, expect, it } from "vitest";
import { findCookieValue, LOCALE_COOKIE, localeCookieWrites, readLocaleCookie, type SupportedLocale } from "./locales";

const SERVED: SupportedLocale[] = ["en", "ja", "zh-CN"];

describe("reading the saved language", () => {
  it.each([
    ["ao_locale=ja", "ja"],
    ["ao_locale=zh-CN", "zh-CN"],
    ["ao_locale=en", "en"],
    ["theme=dark; ao_locale=ja; other=1", "ja"],
    ["  ao_locale = zh-CN  ", "zh-CN"],
    ['ao_locale="ja"', "ja"],
  ])("honours a language the docs serve: %j", (cookie, expected) => {
    expect(readLocaleCookie(cookie, SERVED)).toBe(expected);
  });

  it.each([
    ["ao_locale=fr", "a landing-only language"],
    ["ao_locale=de", "an interface catalog without articles"],
    ["ao_locale=ja-JP", "a near miss"],
    ["ao_locale=JA", "the wrong case"],
    ["ao_locale=", "an empty value"],
    ["ao_locale", "no separator"],
    ['ao_locale="', "a lone quote"],
    ["ao_locale=%E0%A4%A", "broken percent encoding"],
    ["xao_locale=ja", "a longer cookie name"],
    ["", "no cookies"],
  ])("falls back to English for %j (%s)", (cookie) => {
    expect(readLocaleCookie(cookie, SERVED)).toBe("en");
  });

  it("takes the first served value when the browser sends duplicates", () => {
    expect(readLocaleCookie("ao_locale=ja; ao_locale=zh-CN", SERVED)).toBe("ja");
    expect(readLocaleCookie("ao_locale=zh-CN; ao_locale=ja", SERVED)).toBe("zh-CN");
  });

  it("skips an unserved duplicate in either order", () => {
    expect(readLocaleCookie("ao_locale=fr; ao_locale=ja", SERVED)).toBe("ja");
    expect(readLocaleCookie("ao_locale=ja; ao_locale=fr", SERVED)).toBe("ja");
    expect(readLocaleCookie("ao_locale=fr; ao_locale=de", SERVED)).toBe("en");
    expect(readLocaleCookie("ao_locale=; ao_locale=zh-CN", SERVED)).toBe("zh-CN");
  });

  it("stays self-contained so the head script can inline it", () => {
    const inlined = new Function(`return (${findCookieValue.toString()})`)() as typeof findCookieValue;
    expect(inlined("ao_locale=fr; ao_locale=ja", LOCALE_COOKIE, SERVED)).toBe("ja");
  });
});

describe("saving the chosen language", () => {
  it("replaces the landing's domain cookie and removes the old host-only one on the docs", () => {
    expect(localeCookieWrites("en", { hostname: "docs.orchestrator.inc", protocol: "https:" })).toEqual([
      "ao_locale=; path=/; max-age=0; samesite=lax; secure",
      "ao_locale=en; path=/; max-age=31536000; samesite=lax; secure; domain=orchestrator.inc",
    ]);
  });

  it("uses the shared domain on the apex too", () => {
    expect(localeCookieWrites("ja", { hostname: "orchestrator.inc", protocol: "https:" })[1]).toContain(
      "domain=orchestrator.inc",
    );
  });

  it.each(["localhost", "127.0.0.1", "docs-preview.pages.dev", "notorchestrator.inc"])(
    "keeps the cookie host-only on %s",
    (hostname) => {
      expect(localeCookieWrites("zh-CN", { hostname, protocol: "http:" })).toEqual([
        "ao_locale=zh-CN; path=/; max-age=31536000; samesite=lax",
      ]);
    },
  );
});
