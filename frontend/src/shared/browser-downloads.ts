export type BrowserDownloadStatus =
	| "blocked"
	| "progressing"
	| "paused"
	| "completed"
	| "cancelled"
	| "interrupted";

export type BrowserDownload = {
	id: string;
	fileName: string;
	source?: string;
	receivedBytes: number;
	totalBytes: number;
	status: BrowserDownloadStatus;
	active?: boolean;
	resumable?: boolean;
	fileRemovable?: boolean;
	startedAt: number;
	updatedAt: number;
};

export type BrowserDownloadsState = {
	downloads: BrowserDownload[];
	error?: string;
};

export type BrowserDownloadAction = "allow" | "pause" | "resume" | "cancel" | "open" | "show" | "remove";

export type BrowserDownloadActionInput = {
	id: string;
	action: BrowserDownloadAction;
};
