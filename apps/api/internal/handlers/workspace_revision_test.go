package handlers

import (
    "context"
    "errors"
    "os"
    "path/filepath"
    "sync"
    "testing"
)

func TestLocalWorkspaceRevisionRejectsStaleSaveWithoutChangingFile(t *testing.T) {
    dir := t.TempDir()
    name := "src/main.ts"
    if err := os.MkdirAll(filepath.Join(dir, "src"), 0755); err != nil { t.Fatal(err) }
    if err := os.WriteFile(filepath.Join(dir, name), []byte("agent change"), 0644); err != nil { t.Fatal(err) }
    expected := workspaceContentRevision([]byte("before agent"))
    _, err := writeLocalWorkspaceRevision(context.Background(), dir, name, []byte("browser draft"), &expected)
    if !errors.Is(err, errWorkspaceRevisionConflict) { t.Fatalf("want revision conflict, got %v", err) }
    got, err := os.ReadFile(filepath.Join(dir, name))
    if err != nil || string(got) != "agent change" { t.Fatalf("stale edit overwrote file: %q %v", got, err) }
}

func TestLocalWorkspaceRevisionHonorsMatchingPrecondition(t *testing.T) {
    dir := t.TempDir()
    if err := os.WriteFile(filepath.Join(dir, "file.ts"), []byte("old"), 0644); err != nil { t.Fatal(err) }
    expected := workspaceContentRevision([]byte("old"))
    next, err := writeLocalWorkspaceRevision(context.Background(), dir, "file.ts", []byte("new"), &expected)
    if err != nil { t.Fatal(err) }
    if next != workspaceContentRevision([]byte("new")) { t.Fatalf("unexpected revision %q", next) }
    got, err := os.ReadFile(filepath.Join(dir, "file.ts"))
    if err != nil || string(got) != "new" { t.Fatalf("write mismatch: %q %v", got, err) }
}

func TestLocalWorkspaceRevisionMissingFileAndInvalidToken(t *testing.T) {
    dir := t.TempDir()
    expectedMissing := ""
    _, err := writeLocalWorkspaceRevision(context.Background(), dir, "new.txt", []byte("created"), &expectedMissing)
    if err != nil { t.Fatal(err) }
    _, err = writeLocalWorkspaceRevision(context.Background(), dir, "new.txt", []byte("overwrite"), &expectedMissing)
    if !errors.Is(err, errWorkspaceRevisionConflict) { t.Fatalf("expected file-exists conflict; got %v", err) }
    malformed := "../garbage"
    _, err = writeLocalWorkspaceRevision(context.Background(), dir, "new.txt", []byte("overwrite"), &malformed)
    if !errors.Is(err, errInvalidWorkspaceRevision) { t.Fatalf("expected invalid revision; got %v", err) }
}

func TestLocalWorkspaceRevisionConcurrentSavesHaveOneWinner(t *testing.T) {
    dir := t.TempDir()
    if err := os.WriteFile(filepath.Join(dir, "file.ts"), []byte("old"), 0644); err != nil { t.Fatal(err) }
    expected := workspaceContentRevision([]byte("old"))
    errorsCh := make(chan error, 2)
    var wg sync.WaitGroup
    for _, value := range []string{"agent A", "agent B"} {
        wg.Add(1)
        go func(content string) {
            defer wg.Done()
            _, err := writeLocalWorkspaceRevision(context.Background(), dir, "file.ts", []byte(content), &expected)
            errorsCh <- err
        }(value)
    }
    wg.Wait()
    close(errorsCh)
    success, conflicts := 0, 0
    for err := range errorsCh {
        switch {
        case err == nil:
            success++
        case errors.Is(err, errWorkspaceRevisionConflict):
            conflicts++
        default:
            t.Fatalf("unexpected error: %v", err)
        }
    }
    if success != 1 || conflicts != 1 { t.Fatalf("successes=%d conflicts=%d", success, conflicts) }
}
