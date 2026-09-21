package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	agent "github.com/alfredxw/denova/agent"
)

// 自 fork 子进程:在独立进程里持续 append,供父进程模拟 -9 强杀。
const crashChildEnv = "DENOVA_CRASH_RECOVERY_CHILD"

func TestMain(m *testing.M) {
	if dir := os.Getenv(crashChildEnv); dir != "" {
		runCrashChild(dir)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runCrashChild(dir string) {
	store, err := NewStore(dir)
	if err != nil {
		return
	}
	sess, err := store.GetOrCreate("crash-fuzz")
	if err != nil {
		return
	}
	for index := 0; ; index++ {
		if err := sess.Append(agent.UserMessage(fmt.Sprintf("u-%06d", index))); err != nil {
			return
		}
		if err := sess.AppendDisplayEvent(DisplayEvent{
			Role: "assistant", Content: fmt.Sprintf("d-%06d", index), RunID: fmt.Sprintf("run-%06d", index),
		}); err != nil {
			return
		}
	}
}

// TestFuzzCrashSnapshots 强杀后对每个磁盘快照及其任意前缀变体断言可加载。
// jsonl 与 checkpoint 以不同采集时刻配对,覆盖 checkpoint 落后/超前的组合。
func TestFuzzCrashSnapshots(t *testing.T) {
	if testing.Short() {
		t.Skip("fuzz crash snapshots in long mode only")
	}
	workDir := t.TempDir()
	snapshotDir := t.TempDir()

	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "-test.run", "^TestMain$")
	command.Env = append(os.Environ(), crashChildEnv+"="+workDir)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	snapshots := 0
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, pair := range []struct{ source, suffix string }{
			{"crash-fuzz.jsonl", "-journal"},
			{"crash-fuzz.idx.json", "-index"},
		} {
			data, err := os.ReadFile(filepath.Join(workDir, pair.source))
			if err != nil {
				continue
			}
			target := filepath.Join(snapshotDir, fmt.Sprintf("%04d%s", snapshots, pair.suffix))
			if err := os.WriteFile(target, data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		snapshots++
		time.Sleep(2 * time.Millisecond)
	}
	_ = command.Process.Kill()
	_, _ = command.Process.Wait()

	entries, err := os.ReadDir(snapshotDir)
	if err != nil {
		t.Fatal(err)
	}
	journalSnapshots := map[string][]byte{}
	indexSnapshots := map[string][]byte{}
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(snapshotDir, entry.Name()))
		if err != nil {
			continue
		}
		switch {
		case strings.HasSuffix(entry.Name(), "-index"):
			indexSnapshots[strings.TrimSuffix(entry.Name(), "-index")] = data
		case strings.HasSuffix(entry.Name(), "-journal"):
			journalSnapshots[strings.TrimSuffix(entry.Name(), "-journal")] = data
		}
	}

	names := make([]string, 0, len(journalSnapshots))
	for name := range journalSnapshots {
		names = append(names, name)
	}
	sort.Strings(names)

	stride := (len(names) + 29) / 30
	if stride < 1 {
		stride = 1
	}
	checked := 0
	for step := 0; step < len(names); step += stride {
		journalData := journalSnapshots[names[step]]
		for _, cut := range []int{0, 1, 89} {
			if cut > len(journalData)-64 {
				continue
			}
			for _, lag := range []int{0, 5, 20} {
				lagStep := step - lag
				if lagStep < 0 {
					continue
				}
				indexData, hasIndex := indexSnapshots[names[lagStep]]
				if !hasIndex {
					continue
				}
				variant := journalData
				if cut > 0 {
					variant = journalData[:len(journalData)-cut]
				}
				crashDir := t.TempDir()
				if err := os.WriteFile(filepath.Join(crashDir, "crash-fuzz.jsonl"), variant, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(crashDir, "crash-fuzz.idx.json"), indexData, 0o644); err != nil {
					t.Fatal(err)
				}
				checked++
				store, err := NewStore(crashDir)
				if err != nil {
					t.Fatalf("snapshot %s cut %d lag %d: store: %v", names[step], cut, lag, err)
				}
				sess, err := store.Get("crash-fuzz")
				if err != nil {
					t.Fatalf("snapshot %s cut %d lag %d: %v", names[step], cut, lag, err)
				}
				_ = sess
			}
		}
	}
	t.Logf("checked %d crash-state variants across %d snapshots", checked, len(journalSnapshots))
	if checked == 0 {
		t.Fatal("no journal+index snapshot pairs checked")
	}
}
