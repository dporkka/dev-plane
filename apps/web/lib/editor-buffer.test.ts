import assert from "node:assert";
import { describe, it } from "node:test";
import {
  makeEditorBuffer,
  editEditorBuffer,
  acknowledgeSavedContent,
  remoteMatchesBaseline,
} from "./editor-buffer";

describe("CodeMirror workspace drafts", () => {
  it("opens clean and clears the dirty flag when changes are undone", () => {
    const opened = makeEditorBuffer("src/App.tsx", "old", "typescript");
    assert.strictEqual(opened.isDirty, false);
    const changed = editEditorBuffer(opened, "new");
    assert.strictEqual(changed.isDirty, true);
    assert.strictEqual(editEditorBuffer(changed, "old").isDirty, false);
  });

  it("clears dirty state when the exact draft is saved", () => {
    const changed = editEditorBuffer(
      makeEditorBuffer("src/App.tsx", "old", "typescript"),
      "new",
    );
    const saved = acknowledgeSavedContent(changed, "new");
    assert.strictEqual(saved.savedContent, "new");
    assert.strictEqual(saved.isDirty, false);
  });

  it("retains edits typed while a previous draft was saving", () => {
    const original = makeEditorBuffer("src/App.tsx", "old", "typescript");
    const first = editEditorBuffer(original, "first");
    const second = editEditorBuffer(first, "second");
    const acknowledged = acknowledgeSavedContent(second, "first");
    assert.strictEqual(acknowledged.content, "second");
    assert.strictEqual(acknowledged.savedContent, "first");
    assert.strictEqual(acknowledged.isDirty, true);
  });

  it("detects a remote edit made since opening the file", () => {
    const file = editEditorBuffer(
      makeEditorBuffer("src/App.tsx", "base", "typescript"),
      "local",
    );
    assert.strictEqual(remoteMatchesBaseline(file, "base"), true);
    assert.strictEqual(remoteMatchesBaseline(file, "agent edit"), false);
  });
});
