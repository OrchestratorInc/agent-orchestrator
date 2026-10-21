"use client";

import { buttonVariants } from "fumadocs-ui/components/ui/button";
import { Popover, PopoverClose, PopoverContent, PopoverTrigger } from "fumadocs-ui/components/ui/popover";
import { useI18n } from "fumadocs-ui/contexts/i18n";
import type { LanguageSelectProps } from "fumadocs-ui/layouts/shared/slots/language-select";
import { msg } from "@lingui/core/macro";
import { useLingui } from "@lingui/react";

/**
 * The framework's language menu, except each choice also closes the menu:
 * switching languages does not navigate, so nothing else would close it.
 */
export function LanguageSelect({ className, variant = "ghost", children, ...rest }: LanguageSelectProps) {
  const context = useI18n();
  const { i18n } = useLingui();
  const label = i18n._(msg({ message: "Choose a language", context: "language switcher" }));

  return (
    <Popover>
      <PopoverTrigger
        aria-label={label}
        className={[buttonVariants({ variant }), "gap-1.5 p-1.5 data-[state=open]:bg-fd-accent", className]
          .filter(Boolean)
          .join(" ")}
        {...rest}
      >
        {children}
      </PopoverTrigger>
      <PopoverContent className="flex flex-col gap-0.5 p-1">
        <p className="p-2 text-xs font-medium text-fd-muted-foreground">{label}</p>
        {context.locales?.map((item) => (
          <PopoverClose
            key={item.locale}
            type="button"
            className={`px-2 py-1.5 text-start text-sm rounded-lg transition-colors ${
              item.locale === context.locale
                ? "bg-fd-primary/10 text-fd-primary"
                : "text-fd-muted-foreground hover:bg-fd-accent hover:text-fd-accent-foreground"
            }`}
            onClick={() => context.onChange?.(item.locale)}
          >
            {item.name}
          </PopoverClose>
        ))}
      </PopoverContent>
    </Popover>
  );
}
