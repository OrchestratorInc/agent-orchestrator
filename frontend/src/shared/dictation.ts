/** The answer to asking macOS for the speech and microphone access native dictation needs. */
export type DictationAuthorization = {
	granted: boolean;
	/** What to do next when access was refused. */
	message?: string;
};

export type DictationEvent =
	| { type: "listening" }
	/** Loudness in 0...1, one value per 10 ms of audio, oldest first. */
	| { type: "levels"; values: number[] }
	| { type: "text"; text: string }
	/** Listening stopped; the final wording follows as "end". */
	| { type: "finishing" }
	| { type: "end"; text: string }
	| { type: "error"; message: string }
	| { type: "exit" };
