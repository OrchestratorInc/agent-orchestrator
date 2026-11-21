import { setupI18n } from "@lingui/core";
import { I18nProvider } from "@lingui/react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { FrameworkProvider } from "fumadocs-core/framework";
import { I18nProvider as FrameworkI18nProvider } from "fumadocs-ui/contexts/i18n";
import type { ReactNode } from "react";
import { afterEach, beforeAll, describe, expect, it } from "vitest";
import { LOCALE_LABELS, SUPPORTED_LOCALES, type SupportedLocale } from "@/i18n/locales";
import { LocalizedCodeBlock } from "./code-block";
import { LanguageSelect, LanguageSelectText } from "./language-select";

const COPY_LABELS: Partial<Record<SupportedLocale, string>> = { en: "Copy code", ja: "コードをコピー" };

beforeAll(() => {
  // The popover measures its trigger; jsdom has no ResizeObserver.
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
});

afterEach(cleanup);

/** Renders `ui` inside an English-marked article, with the interface in `locale`. */
function renderInArticle(locale: SupportedLocale, ui: ReactNode) {
  const i18n = setupI18n({ locale, messages: { [locale]: {} } });
  return render(
    <FrameworkProvider useRouter={() => ({ push() {}, refresh() {} })} usePathname={() => "/"} useParams={() => ({})}>
      <I18nProvider i18n={i18n}>
        <FrameworkI18nProvider
          locale={locale}
          locales={SUPPORTED_LOCALES.map((code) => ({ locale: code, name: LOCALE_LABELS[code] }))}
          translations={{ "Copy Text(code block)(aria-label)": COPY_LABELS[locale] ?? "Copy code" }}
        >
          <div lang="en">{ui}</div>
        </FrameworkI18nProvider>
      </I18nProvider>
    </FrameworkProvider>,
  );
}

const nearestLang = (element: Element) => element.closest("[lang]")?.getAttribute("lang");

describe("code block language boundaries", () => {
  it.each(["ja", "en"] as const)("keeps the copy button in the %s interface and the code in English", (locale) => {
    renderInArticle(
      locale,
      <LocalizedCodeBlock>
        <code>npm install</code>
      </LocalizedCodeBlock>,
    );

    const copy = screen.getByRole("button", { name: COPY_LABELS[locale] });
    expect(nearestLang(copy)).toBe(locale);
    expect(nearestLang(screen.getByText("npm install"))).toBe("en");
  });
});

describe("language menu", () => {
  it("marks the current language and every choice with its own language", async () => {
    renderInArticle("ja", <LanguageSelect><LanguageSelectText /></LanguageSelect>);

    expect(nearestLang(screen.getByText("日本語"))).toBe("ja");

    const trigger = screen.getByRole("button", { name: "Choose a language" });
    fireEvent.pointerDown(trigger, { button: 0, pointerType: "mouse" });
    fireEvent.click(trigger);

    for (const locale of SUPPORTED_LOCALES) {
      const item = await screen.findByRole("button", { name: LOCALE_LABELS[locale] });
      expect(item.getAttribute("lang")).toBe(locale);
    }
  });
});
