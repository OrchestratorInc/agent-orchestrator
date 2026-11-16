package controllers

import (
	"encoding/base64"
	"testing"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestDecodeSpawnAttachments(t *testing.T) {
	t.Run("nil when empty", func(t *testing.T) {
		out, err := decodeSpawnAttachments(nil)
		if err != nil || out != nil {
			t.Fatalf("want nil,nil got %v,%v", out, err)
		}
	})

	t.Run("decodes and maps extension", func(t *testing.T) {
		out, err := decodeSpawnAttachments([]AttachmentInput{
			{MimeType: "image/png", Data: b64([]byte("pngbytes"))},
			{MimeType: "IMAGE/JPEG", Data: b64([]byte("jpgbytes"))},
		})
		if err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if len(out) != 2 {
			t.Fatalf("want 2 attachments got %d", len(out))
		}
		if out[0].Ext != ".png" || string(out[0].Data) != "pngbytes" {
			t.Errorf("attachment 0 = %q %q", out[0].Ext, out[0].Data)
		}
		if out[1].Ext != ".jpg" {
			t.Errorf("case-insensitive mime not mapped: %q", out[1].Ext)
		}
	})

	t.Run("rejects too many", func(t *testing.T) {
		in := make([]AttachmentInput, maxAttachments+1)
		for i := range in {
			in[i] = AttachmentInput{MimeType: "image/png", Data: b64([]byte("x"))}
		}
		_, err := decodeSpawnAttachments(in)
		if err == nil || err.code != "TOO_MANY_ATTACHMENTS" {
			t.Fatalf("want TOO_MANY_ATTACHMENTS got %v", err)
		}
	})

	t.Run("accepts non-image types", func(t *testing.T) {
		out, err := decodeSpawnAttachments([]AttachmentInput{
			{MimeType: "application/pdf", Data: b64([]byte("pdfbytes"))},
			{MimeType: "text/plain", Data: b64([]byte("textbytes"))},
		})
		if err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if len(out) != 2 {
			t.Fatalf("want 2 attachments got %d", len(out))
		}
		if out[0].Ext != ".pdf" || string(out[0].Data) != "pdfbytes" {
			t.Errorf("attachment 0 = %q %q", out[0].Ext, out[0].Data)
		}
		if out[1].Ext != ".txt" || string(out[1].Data) != "textbytes" {
			t.Errorf("attachment 1 = %q %q", out[1].Ext, out[1].Data)
		}
	})

	t.Run("accepts video above previous file limit", func(t *testing.T) {
		const size = 11 << 20
		out, err := decodeSpawnAttachments([]AttachmentInput{{MimeType: "video/quicktime", Data: b64(make([]byte, size))}})
		if err != nil {
			t.Fatalf("11 MiB video rejected: %+v", err)
		}
		if len(out) != 1 {
			t.Fatalf("decoded video count = %d, want 1", len(out))
		}
		if out[0].Ext != ".mov" || len(out[0].Data) != size {
			t.Fatalf("decoded video extension = %q, bytes = %d", out[0].Ext, len(out[0].Data))
		}
	})

	t.Run("accepts batch above previous total limit", func(t *testing.T) {
		const size = 9 << 20
		data := b64(make([]byte, size))
		out, err := decodeSpawnAttachments([]AttachmentInput{
			{MimeType: "application/octet-stream", Data: data},
			{MimeType: "application/octet-stream", Data: data},
			{MimeType: "application/octet-stream", Data: data},
		})
		if err != nil {
			t.Fatalf("27 MiB batch rejected: %+v", err)
		}
		if len(out) != 3 {
			t.Fatalf("decoded batch count = %d, want 3", len(out))
		}
		for i, attachment := range out {
			if len(attachment.Data) != size {
				t.Fatalf("decoded batch attachment %d = %d bytes, want %d", i, len(attachment.Data), size)
			}
		}
	})

	// SVG is XML that can carry active content; it is explicitly blocked.
	t.Run("rejects svg", func(t *testing.T) {
		_, err := decodeSpawnAttachments([]AttachmentInput{{MimeType: "image/svg+xml", Data: b64([]byte("<svg/>"))}})
		if err == nil || err.code != "UNSUPPORTED_ATTACHMENT_TYPE" {
			t.Fatalf("want UNSUPPORTED_ATTACHMENT_TYPE got %v", err)
		}
	})

	t.Run("rejects invalid base64", func(t *testing.T) {
		_, err := decodeSpawnAttachments([]AttachmentInput{{MimeType: "image/png", Data: "!!!not base64!!!"}})
		if err == nil || err.code != "INVALID_ATTACHMENT_DATA" {
			t.Fatalf("want INVALID_ATTACHMENT_DATA got %v", err)
		}
	})

	t.Run("rejects empty payload", func(t *testing.T) {
		_, err := decodeSpawnAttachments([]AttachmentInput{{MimeType: "image/png", Data: ""}})
		if err == nil || err.code != "INVALID_ATTACHMENT_DATA" {
			t.Fatalf("want INVALID_ATTACHMENT_DATA got %v", err)
		}
	})

	t.Run("names an upload instead of inline data", func(t *testing.T) {
		const upload = "upload-0123456789abcdef0123456789abcdef"
		out, err := decodeSpawnAttachments([]AttachmentInput{{MimeType: "video/mp4", UploadID: upload}})
		if err != nil {
			t.Fatalf("upload rejected: %+v", err)
		}
		if len(out) != 1 || out[0].Upload != upload || out[0].Data != nil || out[0].Ext != ".mp4" {
			t.Fatalf("decoded upload = %+v", out)
		}
	})

	t.Run("rejects a malformed upload id", func(t *testing.T) {
		_, err := decodeSpawnAttachments([]AttachmentInput{{MimeType: "image/png", UploadID: "../attachments/x"}})
		if err == nil || err.code != "INVALID_ATTACHMENT_UPLOAD" {
			t.Fatalf("want INVALID_ATTACHMENT_UPLOAD got %v", err)
		}
	})

	t.Run("rejects data and an upload together", func(t *testing.T) {
		_, err := decodeSpawnAttachments([]AttachmentInput{{MimeType: "image/png", Data: b64([]byte("x")), UploadID: "upload-0123456789abcdef0123456789abcdef"}})
		if err == nil || err.code != "INVALID_ATTACHMENT_DATA" {
			t.Fatalf("want INVALID_ATTACHMENT_DATA got %v", err)
		}
	})

	t.Run("a capped caller takes no uploads", func(t *testing.T) {
		_, err := decodeAttachments([]AttachmentInput{{MimeType: "image/png", UploadID: "upload-0123456789abcdef0123456789abcdef"}}, 10<<20, 25<<20)
		if err == nil || err.code != "INVALID_ATTACHMENT_DATA" {
			t.Fatalf("want INVALID_ATTACHMENT_DATA got %v", err)
		}
	})

	t.Run("a capped caller still refuses oversized inline data", func(t *testing.T) {
		_, err := decodeAttachments([]AttachmentInput{{MimeType: "image/png", Data: b64(make([]byte, 1<<10+1))}}, 1<<10, 4<<10)
		if err == nil || err.code != "ATTACHMENT_TOO_LARGE" {
			t.Fatalf("want ATTACHMENT_TOO_LARGE got %v", err)
		}
	})

	t.Run("handles unknown mime type", func(t *testing.T) {
		out, err := decodeSpawnAttachments([]AttachmentInput{
			{MimeType: "application/octet-stream", Data: b64([]byte("bytes"))},
		})
		if err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if len(out) != 1 {
			t.Fatalf("want 1 attachment got %d", len(out))
		}
		// Should have a fallback extension
		if out[0].Ext == "" {
			t.Errorf("want non-empty extension got %q", out[0].Ext)
		}
	})

	t.Run("handles mime type with suffix", func(t *testing.T) {
		out, err := decodeSpawnAttachments([]AttachmentInput{
			{MimeType: "application/vnd.api+json", Data: b64([]byte("jsonbytes"))},
		})
		if err != nil {
			t.Fatalf("unexpected error: %+v", err)
		}
		if len(out) != 1 {
			t.Fatalf("want 1 attachment got %d", len(out))
		}
		if string(out[0].Data) != "jsonbytes" {
			t.Errorf("attachment data mismatch: %q", out[0].Data)
		}
	})
}

func TestDecodeSpawnAttachmentsTrimsWhitespace(t *testing.T) {
	out, err := decodeSpawnAttachments([]AttachmentInput{
		{MimeType: "  image/png  ", Data: "  " + b64([]byte("hi")) + "  "},
	})
	if err != nil {
		t.Fatalf("unexpected error: %+v", err)
	}
	if len(out) != 1 || string(out[0].Data) != "hi" {
		t.Fatalf("whitespace not trimmed: %+v", out)
	}
}
