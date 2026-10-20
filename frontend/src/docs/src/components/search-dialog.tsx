"use client";

import { useDocsSearch } from "fumadocs-core/search/client";
import { oramaStaticClient } from "fumadocs-core/search/client/orama-static";
import {
  SearchDialog,
  SearchDialogClose,
  SearchDialogContent,
  SearchDialogHeader,
  SearchDialogIcon,
  SearchDialogInput,
  SearchDialogList,
  SearchDialogOverlay,
  type SharedProps,
} from "fumadocs-ui/components/dialog/search";
import { useI18n } from "fumadocs-ui/contexts/i18n";
import { useMemo } from "react";
import { DEFAULT_LOCALE, isSupportedLocale, localizePath } from "@/i18n/locales";

const client = oramaStaticClient({ from: "/api/search" });

/**
 * Searches the one English index every locale shares, then points each
 * result at the reader's locale so search does not drop them into English.
 */
export function LocalizedSearchDialog(props: SharedProps) {
  const { locale: current } = useI18n();
  const locale = current && isSupportedLocale(current) ? current : DEFAULT_LOCALE;
  const { search, setSearch, query } = useDocsSearch({ client });

  const items = useMemo(() => {
    if (query.data === "empty" || !query.data) return null;
    return query.data.map((item) => ({ ...item, url: localizePath(locale, item.url) }));
  }, [locale, query.data]);

  return (
    <SearchDialog search={search} onSearchChange={setSearch} isLoading={query.isLoading} {...props}>
      <SearchDialogOverlay />
      <SearchDialogContent>
        <SearchDialogHeader>
          <SearchDialogIcon />
          <SearchDialogInput />
          <SearchDialogClose />
        </SearchDialogHeader>
        <SearchDialogList items={items} />
      </SearchDialogContent>
    </SearchDialog>
  );
}
