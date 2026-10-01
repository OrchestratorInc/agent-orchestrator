package workerexec

import (
	"bytes"
	"encoding/base64"
	"golang.org/x/image/bmp"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/attachments"
)

func TestNativeAndToolImageInputs(t *testing.T) {
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "one.png"), filepath.Join(dir, "two.png")}
	data := [][]byte{[]byte("image-one"), []byte("image-two")}
	for i, path := range paths {
		if err := os.WriteFile(path, data[i], 0600); err != nil {
			t.Fatal(err)
		}
	}
	metadata := []attachments.Metadata{{MIMEType: "image/png"}, {MIMEType: "image/png"}}
	blocks, err := acpImagePrompt("", paths, metadata, true)
	if err != nil || len(blocks) != 2 {
		t.Fatal("native images", err)
	}
	for i, b := range blocks {
		if b.Image == nil || b.Image.MimeType != "image/png" {
			t.Fatal("missing image block")
		}
		got, _ := base64.StdEncoding.DecodeString(b.Image.Data)
		if !bytes.Equal(got, data[i]) {
			t.Fatal("native image contents differ")
		}
	}
	tool, err := acpImagePrompt("compare", paths, metadata, false)
	if err != nil || len(tool) != 1 || !strings.Contains(tool[0].Text.Text, "image-reading tool") || !strings.Contains(tool[0].Text.Text, paths[1]) {
		t.Fatal("no tool instruction", err)
	}
	if _, err := acpImagePrompt("", nil, metadata, true); err == nil {
		t.Fatal("allowed missing materialization")
	}
	codex := codexImageInput("", paths)
	if len(codex) != 2 || codex[1]["type"] != "localImage" || codex[1]["path"] != paths[1] {
		t.Fatal("codex image-only input", codex)
	}
}

func TestACPNativeBMPUsesPNG(t *testing.T) {
	var encoded bytes.Buffer
	if err := bmp.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "image.bmp")
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	blocks, err := acpImagePrompt("", []string{path}, []attachments.Metadata{{MIMEType: "image/bmp"}}, true)
	if err != nil {
		t.Fatal(err)
	}
	block := blocks[0].Image
	if block.MimeType != "image/png" {
		t.Fatal("BMP sent as unsupported native image")
	}
	data, err := base64.StdEncoding.DecodeString(block.Data)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal("converted image invalid", err)
	}
	original, _ := os.ReadFile(path)
	if !bytes.Equal(original, encoded.Bytes()) {
		t.Fatal("original BMP changed")
	}
}
