package session

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	agent "github.com/alfredxw/denova/agent"
)

// 模拟进程被硬杀:不调用 Session.Close / Store.Close,磁盘状态保持原样。
func hardKillStore(store *Store) { _ = store }

// TestReloadAfterHardKillWithoutClose 重启后 loadSession 必须成功。
func TestReloadAfterHardKillWithoutClose(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetOrCreate("crash-kill")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 140; index++ {
		if err := sess.Append(agent.UserMessage(fmt.Sprintf("m-%03d", index))); err != nil {
			t.Fatal(err)
		}
	}
	hardKillStore(store)

	store2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess2, err := store2.Get("crash-kill")
	if err != nil {
		t.Fatalf("reload after hard kill: %v", err)
	}
	_ = sess2
}

// TestReloadAfterHardKillWithTornTail 在硬杀基础上叠加写一半的尾部行。
func TestReloadAfterHardKillWithTornTail(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetOrCreate("crash-torn")
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 137; index++ {
		if err := sess.Append(agent.UserMessage(fmt.Sprintf("m-%03d", index))); err != nil {
			t.Fatal(err)
		}
	}
	hardKillStore(store)

	// 模拟 append 写到一半进程死亡:追加半行 JSON,无换行。
	filePath := filepath.Join(dir, "crash-torn.jsonl")
	file, err := os.OpenFile(filePath, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"type":"message","message":{"role":"user","con`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	store2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess2, err := store2.Get("crash-torn")
	if err != nil {
		t.Fatalf("reload after torn-tail kill: %v", err)
	}
	_ = sess2
}

// TestReloadAfterHardKillMixedEvents 用 display 事件让 RecentCursors 与
// message locator 的增速不同,覆盖 messageTransactionsBefore 的窗口边界。
func TestReloadAfterHardKillMixedEvents(t *testing.T) {
	dir := t.TempDir()
	store, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := store.GetOrCreate("crash-mixed")
	if err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 30; round++ {
		if err := sess.Append(agent.UserMessage(fmt.Sprintf("u-%03d", round))); err != nil {
			t.Fatal(err)
		}
		for item := 0; item < 10; item++ {
			if err := sess.AppendDisplayEvent(DisplayEvent{
				Role: "assistant", Content: fmt.Sprintf("d-%03d-%02d", round, item), RunID: fmt.Sprintf("run-%03d", round),
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	hardKillStore(store)

	store2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	sess2, err := store2.Get("crash-mixed")
	if err != nil {
		t.Fatalf("reload after mixed-event kill: %v", err)
	}
	_ = sess2
}
