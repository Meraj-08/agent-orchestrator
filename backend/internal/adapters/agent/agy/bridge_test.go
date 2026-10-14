package agy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBridgeConversationToCLIAndACP(t *testing.T) {
	geminiHome := t.TempDir()
	id := "test-conv-1234"

	// 1. Setup an ACP conversation
	acpDir := filepath.Join(geminiHome, "antigravity-acp")
	if err := os.MkdirAll(filepath.Join(acpDir, "conversations"), 0700); err != nil {
		t.Fatal(err)
	}
	acpDB := filepath.Join(acpDir, "conversations", id+".db")
	if err := os.WriteFile(acpDB, []byte("acp-sqlite-data"), 0600); err != nil {
		t.Fatal(err)
	}
	acpMeta := filepath.Join(acpDir, "conversations", id+".meta")
	if err := os.WriteFile(acpMeta, []byte(`{"mode_id":"yolo"}`), 0600); err != nil {
		t.Fatal(err)
	}
	acpBrain := filepath.Join(acpDir, "brain", id)
	if err := os.MkdirAll(acpBrain, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acpBrain, "scratch.txt"), []byte("brain-content"), 0600); err != nil {
		t.Fatal(err)
	}

	// 2. Bridge to CLI
	if err := BridgeConversationToCLI(geminiHome, id); err != nil {
		t.Fatalf("BridgeConversationToCLI: %v", err)
	}

	cliDB := filepath.Join(geminiHome, "antigravity-cli", "conversations", id+".db")
	cliData, err := os.ReadFile(cliDB)
	if err != nil {
		t.Fatalf("read bridged cli db: %v", err)
	}
	if string(cliData) != "acp-sqlite-data" {
		t.Fatalf("got cli db content %q, want acp-sqlite-data", string(cliData))
	}
	acpInfo, err := os.Stat(acpDB)
	if err != nil {
		t.Fatal(err)
	}
	cliInfo, err := os.Stat(cliDB)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(acpInfo, cliInfo) {
		t.Fatal("bridged databases share an inode; expected an independent snapshot")
	}

	cliMeta := filepath.Join(geminiHome, "antigravity-cli", "conversations", id+".meta")
	if _, err := os.Stat(cliMeta); err != nil {
		t.Fatalf("bridged cli meta missing: %v", err)
	}

	cliBrain := filepath.Join(geminiHome, "antigravity-cli", "brain", id)
	if _, err := os.Stat(filepath.Join(cliBrain, "scratch.txt")); err != nil {
		t.Fatalf("bridged cli brain missing: %v", err)
	}

	// 3. Update CLI db and bridge back to ACP
	if err := os.WriteFile(cliDB, []byte("cli-updated-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := BridgeConversationToACP(geminiHome, id); err != nil {
		t.Fatalf("BridgeConversationToACP: %v", err)
	}

	acpUpdated, err := os.ReadFile(acpDB)
	if err != nil {
		t.Fatalf("read bridged acp db: %v", err)
	}
	if string(acpUpdated) != "cli-updated-data" {
		t.Fatalf("got acp db content %q, want cli-updated-data", string(acpUpdated))
	}
}

func TestBridgeRejectsPathTraversalAndCopiesSQLiteSidecars(t *testing.T) {
	home := t.TempDir()
	if err := BridgeConversationToCLI(home, "../outside"); err == nil {
		t.Fatal("expected invalid conversation id error")
	}
	const id = "safe-id"
	src := filepath.Join(home, "antigravity-acp", "conversations")
	if err := os.MkdirAll(src, 0700); err != nil {
		t.Fatal(err)
	}
	for suffix, data := range map[string]string{".db": "database", ".db-wal": "wal", ".db-shm": "shm"} {
		if err := os.WriteFile(filepath.Join(src, id+suffix), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := BridgeConversationToCLI(home, id); err != nil {
		t.Fatal(err)
	}
	for suffix, want := range map[string]string{".db": "database", ".db-wal": "wal", ".db-shm": "shm"} {
		got, err := os.ReadFile(filepath.Join(home, "antigravity-cli", "conversations", id+suffix))
		if err != nil || string(got) != want {
			t.Fatalf("sidecar %s = %q, %v; want %q", suffix, got, err, want)
		}
	}
}

func TestBridgeRequiresSourceDatabase(t *testing.T) {
	home := t.TempDir()
	const id = "conv-missing-src"

	// If destination DB exists but source DB is missing, bridge must fail.
	dst := filepath.Join(home, "antigravity-acp", "conversations")
	if err := os.MkdirAll(dst, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dst, id+".db"), []byte("older-acp-db"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := BridgeConversationToACP(home, id); err == nil {
		t.Fatal("BridgeConversationToACP succeeded with missing source CLI db; want error")
	}

	// Reverse direction: missing ACP source db must also fail.
	cliDst := filepath.Join(home, "antigravity-cli", "conversations")
	if err := os.MkdirAll(cliDst, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cliDst, "other.db"), []byte("older-cli-db"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := BridgeConversationToCLI(home, "other"); err == nil {
		t.Fatal("BridgeConversationToCLI succeeded with missing source ACP db; want error")
	}
}

func TestGeminiHomePrefersSessionThenDaemonThenDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GEMINI_HOME", "")
	if got, _ := GeminiHome(nil); got != filepath.Join(home, ".gemini") {
		t.Errorf("default GeminiHome = %q", got)
	}
	t.Setenv("GEMINI_HOME", "/daemon/home")
	if got, _ := GeminiHome(nil); got != "/daemon/home" {
		t.Errorf("daemon GeminiHome = %q", got)
	}
	if got, _ := GeminiHome(map[string]string{"GEMINI_HOME": "/session/home"}); got != "/session/home" {
		t.Errorf("session GeminiHome = %q", got)
	}
}
