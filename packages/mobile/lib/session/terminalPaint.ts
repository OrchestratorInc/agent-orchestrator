/**
 * Watches a terminal's output for the first byte that draws something.
 *
 * A fresh attach answers at once with the daemon's init handshake and a repaint
 * (see `TERMINAL_RESET` in useTerminalMux), but an agent TUI that is still
 * booting has nothing on screen yet, so "any output" would drop the loader onto
 * an empty grid. Escape sequences, control bytes and spaces paint nothing; the
 * first other byte does. The parser state carries across chunks, because the
 * socket splits sequences wherever it likes and a CSI tail read on its own
 * ("[?25h") would look like text.
 */
export function createPaintDetector(): (bytes: Uint8Array) => boolean {
	const ESC = 0x1b;
	let state: "text" | "esc" | "csi" | "string" | "stringEsc" = "text";
	return (bytes) => {
		for (const byte of bytes) {
			switch (state) {
				case "text":
					if (byte === ESC) state = "esc";
					else if (byte > 0x20 && byte !== 0x7f) return true;
					break;
				case "esc":
					// CSI, then OSC/DCS/SOS/PM/APC (terminated by BEL or ST); any
					// other byte completes a two-byte escape.
					if (byte === 0x5b) state = "csi";
					else if (byte === 0x5d || byte === 0x50 || byte === 0x58 || byte === 0x5e || byte === 0x5f) state = "string";
					else state = "text";
					break;
				case "csi":
					if (byte >= 0x40 && byte <= 0x7e) state = "text";
					break;
				case "string":
					if (byte === 0x07) state = "text";
					else if (byte === ESC) state = "stringEsc";
					break;
				case "stringEsc":
					state = byte === 0x5c ? "text" : "string";
					break;
			}
		}
		return false;
	};
}
