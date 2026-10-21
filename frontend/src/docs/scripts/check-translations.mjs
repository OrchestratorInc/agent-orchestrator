// Checks the translated articles in content/. A translation is `page.<locale>.mdx`
// beside the English `page.mdx`, and its frontmatter records the English file
// it was translated from:
//
//   translatedFrom: <first 12 hex characters of the English file's SHA-256>
//
// Fails when a translation has no English article, no `translatedFrom`, or its
// language has no route. Warns when the English article changed since the
// translation, so the translation can be brought up to date.
import { createHash } from "node:crypto";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, relative } from "node:path";

const root = new URL("..", import.meta.url).pathname;
const contentDir = join(root, "content");

const localesSource = readFileSync(join(root, "src/i18n/locales.ts"), "utf8");
const localeList = localesSource.match(/SUPPORTED_LOCALES = \[([^\]]*)\]/)?.[1];
if (!localeList) throw new Error("Could not read SUPPORTED_LOCALES from src/i18n/locales.ts");
const locales = [...localeList.matchAll(/"([^"]+)"/g)].map((match) => match[1]).filter((locale) => locale !== "en");

function* walk(dir) {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) yield* walk(path);
    else yield path;
  }
}

function sourceHash(text) {
  return createHash("sha256").update(text).digest("hex").slice(0, 12);
}

const errors = [];
const warnings = [];
const translatedLocales = new Set();

for (const file of walk(contentDir)) {
  const match = file.match(/^(.*)\.([^./]+)\.mdx$/);
  if (!match || !locales.includes(match[2])) continue;
  const [, base, locale] = match;
  const name = relative(root, file);
  translatedLocales.add(locale);

  const englishFile = `${base}.mdx`;
  if (!existsSync(englishFile)) {
    errors.push(`${name}: no English article at ${relative(root, englishFile)}`);
    continue;
  }
  const recorded = readFileSync(file, "utf8").match(/^---\n[\s\S]*?^translatedFrom:\s*(\S+)\s*$[\s\S]*?^---$/m)?.[1];
  const current = sourceHash(readFileSync(englishFile, "utf8"));
  if (!recorded) {
    errors.push(`${name}: add "translatedFrom: ${current}" to the frontmatter`);
  } else if (recorded !== current) {
    warnings.push(
      `${name}: ${relative(root, englishFile)} changed since it was translated. ` +
        `Update the translation, then set "translatedFrom: ${current}".`,
    );
  }
}

for (const locale of locales) {
  const routed = existsSync(join(root, `src/app/(${locale})/${locale}/[[...slug]]/page.tsx`));
  if (translatedLocales.has(locale) && !routed) {
    errors.push(`${locale} has translated articles but no route. Copy src/app/(ja) to src/app/(${locale}) and rename.`);
  }
  if (!translatedLocales.has(locale) && routed) {
    errors.push(`src/app/(${locale}) exists but no article is translated into ${locale}. Remove it.`);
  }
}

for (const warning of warnings) console.warn(`warning: ${warning}`);
for (const error of errors) console.error(`error: ${error}`);
if (errors.length) process.exit(1);
console.log(`Checked translations in ${translatedLocales.size} languages.`);
