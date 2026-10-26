import { ChatImage } from "./ChatImage";

type ToolImagePart = { kind: "image"; src?: string; label: string };
type ToolContentPart = { kind: "text"; text: string } | ToolImagePart;

function toolImage(record: Record<string, unknown>, truncated: boolean): ToolImagePart | undefined {
	const source = record.source && typeof record.source === "object"
		? record.source as Record<string, unknown> : undefined;
	const imageSource = typeof source?.media_type === "string" && /^image\//i.test(source.media_type);
	if (record.type !== "image" && !imageSource) return undefined;
	const data = source?.data ?? record.data;
	const mime = source?.media_type ?? record.mimeType;
	// Only a bounded MIME token is used in visible labels; never echo arbitrary payload fields.
	const type = typeof mime === "string" && /^image\/[a-z0-9.+-]{1,64}$/i.test(mime) ? mime : "image/unknown";
	const size = typeof data === "string" ? `${data.length} base64 characters`
		: typeof record.bytes === "number" && Number.isFinite(record.bytes) && record.bytes >= 0
			? `${record.bytes} bytes` : "size unavailable";
	const label = `Tool image (${type}, ${size})`;
	const valid = !truncated && source?.truncated !== true && type !== "image/unknown" && typeof data === "string" && data.length > 0 &&
		data.length % 4 === 0 && /^[A-Za-z0-9+/]*={0,2}$/.test(data);
	return { kind: "image", src: valid ? `data:${type};base64,${data}` : undefined, label };
}

/** Split binary blocks before text extraction: image data must never reach a pre/JSON fallback. */
export function normalizeToolContent(value: unknown, truncated = false): ToolContentPart[] {
	if (typeof value === "string") return value ? [{ kind: "text", text: value }] : [];
	if (Array.isArray(value)) return value.flatMap((item) => normalizeToolContent(item, truncated));
	if (!value || typeof value !== "object") return [];
	const record = value as Record<string, unknown>;
	const incomplete = truncated || record.truncated === true;
	const parts: ToolContentPart[] = [];
	if (typeof record.text === "string" && record.text) parts.push({ kind: "text", text: record.text });
	const image = toolImage(record, incomplete);
	if (image) parts.push(image);
	for (const key of ["content", "output"]) {
		parts.push(...normalizeToolContent(record[key], incomplete));
	}
	return parts;
}

/** Preserve arbitrary MCP result fields while removing image bytes from the JSON text view. */
export function splitToolResult(value: unknown): { images: ToolImagePart[]; value: unknown } {
	const images: ToolImagePart[] = [];
	function sanitize(item: unknown, truncated = false): unknown {
		if (Array.isArray(item)) return item.map((child) => sanitize(child, truncated));
		if (!item || typeof item !== "object") return item;
		const record = item as Record<string, unknown>;
		const incomplete = truncated || record.truncated === true;
		const image = toolImage(record, incomplete);
		if (image) {
			images.push(image);
			const rest = Object.fromEntries(Object.entries(record).filter(([key]) =>
				!["type", "data", "mimeType", "source"].includes(key)));
			return { ...(sanitize(rest, incomplete) as Record<string, unknown>), type: "image", imagePlaceholder: image.label };
		}
		return Object.fromEntries(Object.entries(record).map(([key, child]) => [key, sanitize(child, incomplete)]));
	}
	return { images, value: sanitize(value) };
}

export function ToolImage({ image }: { image: ToolImagePart }) {
	// ChatImage supplies the same size bounds, zoom dialog and failed-decode fallback as message images.
	return <ChatImage src={image.src} alt={image.label} />;
}
