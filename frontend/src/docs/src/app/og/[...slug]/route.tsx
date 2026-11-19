import { readFile } from "node:fs/promises";
import { join } from "node:path";
import { getBreadcrumbItems } from "fumadocs-core/breadcrumb";
import { ImageResponse } from "next/og";
import { notFound } from "next/navigation";
import type { CSSProperties } from "react";
import { DEFAULT_LOCALE } from "@/i18n/locales";
import { visibleProductName } from "@/lib/branding";
import { COMPANY } from "@/lib/constants";
import { docsTree } from "@/lib/docs-tree";
import { OG_IMAGE_SIZE } from "@/lib/seo";
import { source } from "@/lib/source";

export const dynamic = "force-static";
export const dynamicParams = false;

type Props = { params: Promise<{ slug: string[] }> };

/**
 * Text wrapped to `width`. The image renderer measures Geist words slightly
 * off, so words separated by plain spaces sit unevenly; each line is one run
 * joined by no-break spaces instead, broken at an estimated width.
 */
function Lines({ text, fontSize, width, style }: { text: string; fontSize: number; width: number; style?: CSSProperties }) {
  // Geist averages a little under half an em per character.
  const perLine = Math.floor(width / (fontSize * 0.5));
  const lines: string[] = [];
  for (const word of text.split(/\s+/)) {
    const last = lines.at(-1);
    if (last !== undefined && last.length + 1 + word.length <= perLine) lines[lines.length - 1] = `${last}\u00a0${word}`;
    else lines.push(word);
  }
  return (
    <div style={{ display: "flex", flexDirection: "column", width, fontSize, ...style }}>
      {lines.map((line, index) => (
        <div key={index} style={{ display: "flex" }}>
          {line}
        </div>
      ))}
    </div>
  );
}

/** One preview image per English article, at `/og/<path>/image.png`. */
export function generateStaticParams() {
  return source.getPages(DEFAULT_LOCALE).map((page) => ({ slug: [...page.slugs, "image.png"] }));
}

const fonts = join(process.cwd(), "node_modules/geist/dist/fonts/geist-sans");
// The same logo the repository uses everywhere else.
const logo = join(process.cwd(), "../../assets/ao-logo.svg");

export async function GET(_request: Request, props: Props) {
  const { slug } = await props.params;
  const page = slug.at(-1) === "image.png" ? source.getPage(slug.slice(0, -1), DEFAULT_LOCALE) : undefined;
  if (!page) notFound();

  const [regular, semiBold, logoSvg] = await Promise.all([
    readFile(join(fonts, "Geist-Regular.ttf")),
    readFile(join(fonts, "Geist-SemiBold.ttf")),
    readFile(logo, "utf8"),
  ]);
  const title = visibleProductName(page.data.title);
  const description = page.data.description && visibleProductName(page.data.description);
  // The sidebar folder the article sits in, if any.
  const [folder] = getBreadcrumbItems(page.url, docsTree(DEFAULT_LOCALE));
  const section = typeof folder?.name === "string" ? folder.name : undefined;

  return new ImageResponse(
    (
      <div
        style={{
          width: "100%",
          height: "100%",
          display: "flex",
          flexDirection: "column",
          justifyContent: "space-between",
          padding: "72px 80px",
          background: "#141412",
          color: "#fbfbf8",
          fontFamily: "Geist",
          borderTop: "8px solid #d25611",
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 20 }}>
          <img src={`data:image/svg+xml;base64,${Buffer.from(logoSvg).toString("base64")}`} width={56} height={56} alt="" />
          <div style={{ display: "flex", fontSize: 32, fontWeight: 600 }}>{`${COMPANY.NAME}\u00a0Docs`}</div>
          {section && <div style={{ display: "flex", fontSize: 28, color: "#e8804a" }}>{`/\u00a0${section.replaceAll(" ", "\u00a0")}`}</div>}
        </div>
        <div style={{ display: "flex", flexDirection: "column", gap: 28 }}>
          <Lines
            text={title}
            fontSize={title.length > 40 ? 64 : 80}
            width={1040}
            style={{ fontWeight: 600, lineHeight: 1.05, letterSpacing: -2 }}
          />
          {description && <Lines text={description} fontSize={32} width={980} style={{ lineHeight: 1.35, color: "#b4b4a8" }} />}
        </div>
        <div style={{ display: "flex", fontSize: 26, color: "#b4b4a8" }}>{new URL(COMPANY.DOCS_URL).host}</div>
      </div>
    ),
    {
      ...OG_IMAGE_SIZE,
      fonts: [
        { name: "Geist", data: regular, weight: 400, style: "normal" },
        { name: "Geist", data: semiBold, weight: 600, style: "normal" },
      ],
    },
  );
}
