export interface CloudCpSseFrame {
	id?: string;
	event?: string;
	data: string;
}

export interface CloudCpSseFrameParser {
	push(chunk: string): CloudCpSseFrame[];
	flush(): CloudCpSseFrame[];
}

function parseBlock(block: string): CloudCpSseFrame | null {
	let id: string | undefined;
	let event: string | undefined;
	const data: string[] = [];
	for (const line of block.split("\n")) {
		if (line === "" || line.startsWith(":")) continue;
		const colon = line.indexOf(":");
		const field = colon === -1 ? line : line.slice(0, colon);
		let value = colon === -1 ? "" : line.slice(colon + 1);
		if (value.startsWith(" ")) value = value.slice(1);
		if (field === "id") {
			id = value;
		} else if (field === "event") {
			event = value;
		} else if (field === "data") {
			data.push(value);
		}
	}
	if (data.length === 0) return null;
	return { id, event, data: data.join("\n") };
}

export function createSseFrameParser(): CloudCpSseFrameParser {
	let buffer = "";

	return {
		push(chunk: string): CloudCpSseFrame[] {
			buffer += chunk;
			let normalized = buffer;
			let heldCr = "";
			if (normalized.endsWith("\r")) {
				heldCr = "\r";
				normalized = normalized.slice(0, -1);
			}
			normalized = normalized.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
			const blocks = normalized.split("\n\n");
			buffer = blocks.pop()! + heldCr;
			const frames: CloudCpSseFrame[] = [];
			for (const block of blocks) {
				const frame = parseBlock(block);
				if (frame !== null) frames.push(frame);
			}
			return frames;
		},

		flush(): CloudCpSseFrame[] {
			const rest = buffer.replace(/\r\n/g, "\n").replace(/\r/g, "\n");
			buffer = "";
			if (rest === "") return [];
			const frame = parseBlock(rest);
			return frame === null ? [] : [frame];
		},
	};
}
