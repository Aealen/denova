package session

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	agent "github.com/alfredxw/denova/agent"

	"denova/internal/agents/conversationjournal"
)

// fakeIndexBody/fakeIndexFile 镜像 conversationjournal 的 sidecar 布局,
// 用于构造通过 checksum 校验但内部错位的 checkpoint。
type fakeIndexBody struct {
	Version             int                            `json:"version"`
	Identity            conversationjournal.Identity   `json:"identity"`
	VerifiedBytes       int64                          `json:"verified_bytes"`
	Cursor              conversationjournal.Cursor     `json:"cursor"`
	TransactionCount    int64                          `json:"transaction_count"`
	Tail                conversationjournal.Location   `json:"tail"`
	TailRecordSHA256    string                         `json:"tail_record_sha256,omitempty"`
	InitialRecordSHA256 string                         `json:"initial_record_sha256,omitempty"`
	NeedsNewline        bool                           `json:"needs_newline,omitempty"`
	Sparse              []conversationjournal.Location `json:"sparse,omitempty"`
	Recent              []conversationjournal.Location `json:"recent,omitempty"`
	Projection          json.RawMessage                `json:"projection"`
}

type fakeIndexFile struct {
	fakeIndexBody
	Checksum string `json:"checksum"`
}

// TestLoadSessionSelfHealsMisalignedCheckpoint 复现线上
// "session canonical message transaction missing" 崩溃:sidecar 的
// message locator 指向 canonical journal 中不存在的 cursor。
// 期望:loadSession 丢弃错位 checkpoint,从 canonical 全量重建。
func TestLoadSessionSelfHealsMisalignedCheckpoint(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetOrCreate("self-heal")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if err := sess.Append(agent.UserMessage(fmt.Sprintf("m-%d", index))); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	// 篡改 sidecar:recent_cursors 抬高 startCursor,并注入一个
	// 超出 canonical head 的 message locator(cursor 49,实际只有 4 行)。
	indexPath := filepath.Join(dir, "self-heal.idx.json")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	var parsed fakeIndexFile
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatal(err)
	}
	var projection map[string]any
	if err := json.Unmarshal(parsed.Projection, &projection); err != nil {
		t.Fatal(err)
	}
	projection["recent_cursors"] = []any{float64(50), float64(51)}
	projection["message_transaction_locators"] = []any{
		map[string]any{"index": float64(0), "cursor": float64(49)},
	}
	projection["message_locators"] = []any{
		map[string]any{"index": float64(0), "cursor": float64(49)},
	}
	tampered, err := json.Marshal(projection)
	if err != nil {
		t.Fatal(err)
	}
	parsed.Projection = tampered
	bodyJSON, err := json.Marshal(parsed.fakeIndexBody)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(bodyJSON)
	parsed.Checksum = hex.EncodeToString(digest[:])
	encoded, err := json.MarshalIndent(parsed, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(indexPath, append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.Get("self-heal")
	if err != nil {
		t.Fatalf("loadSession must self-heal a misaligned checkpoint: %v", err)
	}
	messages, _, _ := recovered.MessageWindow()
	if len(messages) != 3 || messages[0].Content != "m-0" || messages[2].Content != "m-2" {
		t.Fatalf("canonical transcript lost after self-heal: %d messages", len(messages))
	}

	// 自愈后 sidecar 必须以正确状态重新落盘,后续加载不再走 rebuild。
	again, err := reopened.Get("self-heal")
	if err != nil {
		t.Fatalf("second load after self-heal: %v", err)
	}
	if again == nil {
		t.Fatal("second load returned nil session")
	}
}
