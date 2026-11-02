export type DictationEvent =
	| { type: "listening" }
	/** Loudness in 0...1, one value per 10 ms of audio, oldest first. */
	| { type: "levels"; values: number[] }
	| { type: "text"; text: string }
	| { type: "end"; text: string }
	| { type: "error"; message: string }
	| { type: "exit" };
