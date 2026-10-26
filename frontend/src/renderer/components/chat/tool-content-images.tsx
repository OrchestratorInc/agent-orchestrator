import { ChatImage } from "./ChatImage";

type ToolImagePart = { kind: "image"; src?: string; label: string };
type ToolContentPart = { kind: "text"; text: string } | ToolImagePart;

/** Split binary blocks before text extraction: image data must never reach a pre/JSON fallback. */
export function normalizeToolContent(value: unknown, truncated = false): ToolContentPart[] {
	if (typeof value === "string") return value ? [{ kind: "text", text: value }] : [];
	if (Array.isArray(value)) return value.flatMap((item) => normalizeToolContent(item, truncated));
	if (!value || typeof value !== "object") return [];
	const record = value as Record<string, unknown>;
	const incomplete = truncated || record.truncated === true;
	const source = record.source && typeof record.source === "object"
		? record.source as Record<string, unknown> : undefined;
	const image = record.type === "image" || source && ("data" in source || "media_type" in source);
	const parts: ToolContentPart[] = [];
	if (typeof record.text === "string" && record.text) parts.push({ kind: "text", text: record.text });
	if (image) {
		const data = source?.data ?? record.data;
		const mime = source?.media_type ?? record.mimeType;
		// Only a bounded MIME token is used in visible labels; never echo arbitrary payload fields.
		const type = typeof mime === "string" && /^image\/[a-z0-9.+-]{1,64}$/i.test(mime) ? mime : "image/unknown";
		const size = typeof data === "string" ? `${data.length} base64 characters`
			: typeof record.bytes === "number" && Number.isFinite(record.bytes) && record.bytes >= 0
				? `${record.bytes} bytes` : "size unavailable";
		const label = `Tool image (${type}, ${size})`;
		const valid = !incomplete && source?.truncated !== true && type !== "image/unknown" && typeof data === "string" && data.length > 0 &&
			data.length % 4 === 0 && /^[A-Za-z0-9+/]*={0,2}$/.test(data);
		parts.push({ kind: "image", src: valid ? `data:${type};base64,${data}` : undefined, label });
	}
	for (const key of ["content", "output"]) {
		parts.push(...normalizeToolContent(record[key], incomplete));
	}
	return parts;
}

export function ToolImage({ image }: { image: ToolImagePart }) {
	// ChatImage supplies the same size bounds, zoom dialog and failed-decode fallback as message images.
	return <ChatImage src={image.src} alt={image.label} />;
}
