package tools

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testZip(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("dist/index.html")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte("<h1>hi</h1>"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestStageStaticSiteArchiveNeedsExactlyOneSource(t *testing.T) {
	files := []staticSiteFileArgs{{Path: "index.html", Content: "hi"}}
	for name, args := range map[string]deployStaticSiteArgs{
		"none":             {},
		"files and zip":    {Files: files, Archive: "UEsDBA=="},
		"zip and zip path": {Archive: "UEsDBA==", ArchivePath: "/tmp/site.zip"},
	} {
		if _, _, msg := stageStaticSiteArchive(args); msg == "" {
			t.Errorf("%s: accepted", name)
		}
	}
	path, _, msg := stageStaticSiteArchive(deployStaticSiteArgs{Files: files})
	if msg != "" || path != "" {
		t.Fatalf("inline files: path=%q msg=%q", path, msg)
	}
	if _, _, msg := stageStaticSiteArchive(deployStaticSiteArgs{Files: files, ArchiveKey: ".uploads/a.zip"}); msg == "" {
		t.Error("files and archive key: accepted")
	}
}

func TestStageStaticSiteArchiveKey(t *testing.T) {
	path, _, msg := stageStaticSiteArchive(deployStaticSiteArgs{ArchiveKey: ".uploads/0123abcd.zip"})
	if msg != "" || path != "" {
		t.Fatalf("staged key: path=%q msg=%q", path, msg)
	}
	// Anything outside the staging prefix would have the server expand and
	// then delete one of the site's own objects, so it is refused up front.
	for _, key := range []string{"index.html", "assets/site.zip", ".uploads/site.tar"} {
		if _, _, msg := stageStaticSiteArchive(deployStaticSiteArgs{ArchiveKey: key}); msg == "" {
			t.Errorf("%s: accepted", key)
		}
	}
}

func TestStageStaticSiteArchiveInline(t *testing.T) {
	data := testZip(t)
	encoded := base64.StdEncoding.EncodeToString(data)
	// Wrapped the way `base64` on the command line emits it.
	var wrapped strings.Builder
	for i := 0; i < len(encoded); i += 76 {
		wrapped.WriteString(encoded[i:min(i+76, len(encoded))] + "\n")
	}

	path, cleanup, msg := stageStaticSiteArchive(deployStaticSiteArgs{Archive: wrapped.String()})
	if msg != "" {
		t.Fatal(msg)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("staged archive differs: %v", err)
	}
	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("staged archive not removed: %v", err)
	}
}

func TestStageStaticSiteArchiveRejectsBadInline(t *testing.T) {
	for name, archive := range map[string]string{
		"not base64": "not base64!",
		"not a zip":  base64.StdEncoding.EncodeToString([]byte("<html></html>")),
		"too large":  strings.Repeat("A", (maxInlineArchiveBytes/3+2)*4),
	} {
		if _, _, msg := stageStaticSiteArchive(deployStaticSiteArgs{Archive: archive}); msg == "" {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestStageStaticSiteArchivePath(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "site.zip")
	os.WriteFile(zipPath, testZip(t), 0o600)
	emptyPath := filepath.Join(dir, "empty.zip")
	os.WriteFile(emptyPath, nil, 0o600)

	path, _, msg := stageStaticSiteArchive(deployStaticSiteArgs{ArchivePath: zipPath})
	if msg != "" || path != zipPath {
		t.Fatalf("path=%q msg=%q", path, msg)
	}
	for name, p := range map[string]string{
		"missing":   filepath.Join(dir, "missing.zip"),
		"empty":     emptyPath,
		"not a zip": filepath.Join(dir, "site.tar"),
		"directory": dir + ".zip",
	} {
		if name == "directory" {
			os.Mkdir(p, 0o700)
		}
		if _, _, msg := stageStaticSiteArchive(deployStaticSiteArgs{ArchivePath: p}); msg == "" {
			t.Errorf("%s: accepted", name)
		}
	}
}
