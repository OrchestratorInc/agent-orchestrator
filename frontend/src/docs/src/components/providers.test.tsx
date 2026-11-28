import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ComponentProps } from "react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import type { SupportedLocale } from "@/i18n/locales";
import { LanguageSelect, LanguageSelectText } from "./language-select";
import { Providers } from "./providers";

vi.mock("next/navigation", () => ({ usePathname: () => "/cli/" }));

// The Next.js provider needs a mounted app router; the framework-neutral one
// with a stub router renders the same language context.
vi.mock("fumadocs-ui/provider/next", async () => {
  const { FrameworkProvider } = await import("fumadocs-core/framework");
  const { RootProvider } = await import("fumadocs-ui/provider/base");
  return {
    RootProvider: (props: ComponentProps<typeof RootProvider>) => (
      <FrameworkProvider useRouter={() => ({ push() {}, refresh() {} })} usePathname={() => "/cli/"} useParams={() => ({})}>
        <RootProvider {...props} />
      </FrameworkProvider>
    ),
  };
});

const SERVED: SupportedLocale[] = ["en", "ja", "zh-CN"];

beforeAll(() => {
  // The popover measures its trigger; jsdom has no ResizeObserver.
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  };
});

function clearCookies() {
  for (const part of document.cookie.split(";")) {
    const name = part.split("=")[0].trim();
    if (name) document.cookie = `${name}=; path=/; max-age=0`;
  }
}

beforeEach(() => {
  clearCookies();
  document.documentElement.lang = "en";
});
afterEach(() => {
  cleanup();
  clearCookies();
});

async function renderEnglishPage() {
  await act(async () => {
    render(
      <Providers servedLocales={SERVED}>
        <LanguageSelect>
          <LanguageSelectText data-testid="current" />
        </LanguageSelect>
      </Providers>,
    );
  });
}

const current = () => screen.getByTestId("current");

describe("language switcher on an English page", () => {
  it.each(["fr", "de", "%E0%A4%A"])("shows English when the saved language %j is not served", async (saved) => {
    document.cookie = `ao_locale=${saved}; path=/`;
    await renderEnglishPage();

    expect(current().textContent).toBe("English");
    expect(current().getAttribute("lang")).toBe("en");
    expect(document.documentElement.lang).toBe("en");
  });

  it.each([
    ["ja", "日本語"],
    ["zh-CN", "简体中文"],
  ])("shows the served language %s", async (saved, label) => {
    document.cookie = `ao_locale=${saved}; path=/`;
    await renderEnglishPage();

    expect(current().textContent).toBe(label);
    expect(document.documentElement.lang).toBe(saved);
  });

  it("offers only the languages the docs serve, and saves the choice", async () => {
    document.cookie = "ao_locale=fr; path=/";
    await renderEnglishPage();

    const trigger = screen.getByRole("button", { name: "Choose a language" });
    fireEvent.pointerDown(trigger, { button: 0, pointerType: "mouse" });
    fireEvent.click(trigger);
    const items = await screen.findAllByRole("button", { name: /English|日本語|简体中文|Français/ });
    expect(items.map((item) => item.getAttribute("lang"))).toEqual(SERVED);

    await act(async () => fireEvent.click(screen.getByRole("button", { name: "简体中文" })));

    expect(document.cookie).toBe("ao_locale=zh-CN");
    expect(current().textContent).toBe("简体中文");
    expect(document.documentElement.lang).toBe("zh-CN");
  });
});
