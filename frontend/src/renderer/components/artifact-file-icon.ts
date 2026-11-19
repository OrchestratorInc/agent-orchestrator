import { Archive, ChartBar, Code2, Database, File, FileCode2, FileText, Image, Music, Presentation, Table2, Video, type AppIcon } from "@/components/app-icons";
import type { ArtifactKind } from "../types/workspace";
import { isDelimitedTextPath } from "../lib/delimited-text";
import { fileMediaKind } from "../lib/file-media";
import { isMermaidPath, isRasterImagePath, isSvgPath } from "../lib/image-files";
import { isMarkdownPreviewPath } from "../lib/markdown-frontmatter";

const CODE = /\.(?:[cm]?[jt]sx?|py|go|rs|rb|java|kt|swift|c|cc|cpp|h|hpp|cs|php|sh|bash|zsh|sql|css|scss|diff|patch)$/i;
const DATA = /\.(?:json|jsonl|ya?ml|toml|ini|conf|xml|env)$/i;
const SPREADSHEET = /\.(?:xlsx?|ods|numbers)$/i;
const SLIDES = /\.(?:pptx?|key|odp)$/i;
const ARCHIVE = /\.(?:zip|tar|t?gz|bz2|xz|7z|rar)$/i;

/**
 * The icon that names an artifact's file type wherever artifacts are listed:
 * the Summary list, the Artifacts page and gallery cards. Pages and Markdown
 * come from the file's kind; everything else from its extension. PDFs and
 * unknown files share the plain file icon.
 */
export function artifactFileIcon(artifact: { kind: ArtifactKind; path: string }): AppIcon {
	const { kind, path } = artifact;
	// Not a globe: that marks a published artifact beside its time.
	if (kind === "html" || /\.html?$/i.test(path)) return FileCode2;
	if (kind === "markdown" || isMarkdownPreviewPath(path)) return FileText;
	if (isRasterImagePath(path) || isSvgPath(path)) return Image;
	const media = fileMediaKind(path);
	if (media === "video") return Video;
	if (media === "audio") return Music;
	if (isMermaidPath(path)) return ChartBar;
	if (isDelimitedTextPath(path) || SPREADSHEET.test(path)) return Table2;
	if (SLIDES.test(path)) return Presentation;
	if (ARCHIVE.test(path)) return Archive;
	if (DATA.test(path)) return Database;
	if (CODE.test(path)) return Code2;
	return File;
}
