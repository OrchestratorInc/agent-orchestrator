import { notFound } from "next/navigation";
import { DEFAULT_LOCALE } from "@/i18n/locales";
import { markdownPath, pageMarkdown } from "@/lib/llms";
import { source } from "@/lib/source";

type Context = { params: Promise<{ slug: string[] }> };

export const dynamic = "force-static";
export const dynamicParams = false;

// Builds `/markdown/quickstart.md` for each English article. The build moves
// them to the site root, so the article `/quickstart/` has `/quickstart.md`.
export async function GET(_request: Request, context: Context) {
  const { slug } = await context.params;
  const path = `/${slug.join("/")}`;
  const page = source.getPages(DEFAULT_LOCALE).find((candidate) => markdownPath(candidate.slugs) === path);
  if (!page) notFound();
  return new Response(await pageMarkdown(page), { headers: { "Content-Type": "text/markdown; charset=utf-8" } });
}

export function generateStaticParams() {
  return source.getPages(DEFAULT_LOCALE).map((page) => ({ slug: markdownPath(page.slugs).slice(1).split("/") }));
}
