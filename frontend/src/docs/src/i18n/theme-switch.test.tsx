import { setupI18n } from "@lingui/core";
import { cleanup, render, screen } from "@testing-library/react";
import { FrameworkProvider } from "fumadocs-core/framework";
import { I18nProvider as FrameworkI18nProvider } from "fumadocs-ui/contexts/i18n";
import { ThemeSwitch } from "fumadocs-ui/layouts/shared/slots/theme-switch";
import { afterEach, describe, expect, it } from "vitest";
import { baseOptions } from "@/lib/layout.shared";
import { frameworkTranslations } from "./ui-messages";

afterEach(cleanup);

describe("theme switch", () => {
  const i18n = setupI18n({ locale: "en", messages: { en: {} } });

  it("offers light, dark and the system theme", () => {
    expect(baseOptions(i18n).themeSwitch).toEqual({ enabled: true, mode: "light-dark-system" });
  });

  it("labels each button with our translated strings", () => {
    // The keys must match what the framework's switch looks up, or it falls
    // back to its untranslated English labels.
    render(
      <FrameworkProvider useRouter={() => ({ push() {}, refresh() {} })} usePathname={() => "/"} useParams={() => ({})}>
        <FrameworkI18nProvider locale="en" translations={frameworkTranslations(i18n)}>
          <ThemeSwitch mode="light-dark-system" />
        </FrameworkI18nProvider>
      </FrameworkProvider>,
    );
    for (const name of ["Light theme", "Dark theme", "Use system theme"]) {
      expect(screen.getByRole("button", { name })).toBeTruthy();
    }
  });
});
