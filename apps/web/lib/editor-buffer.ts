/** Last known server state and current browser draft of a workspace file. */
export interface EditorBuffer {
  path: string;
  language: "typescript" | "javascript" | "json" | "markdown";
  content: string;
  savedContent: string;
  /** Revision returned by the workspace API, not an editor change counter. */
  revision?: string;
  isDirty: boolean;
}

export function makeEditorBuffer(
  path: string,
  content: string,
  language: EditorBuffer["language"],
  revision?: string,
): EditorBuffer {
  return { path, content, savedContent: content, language, revision, isDirty: false };
}

export function editEditorBuffer(
  file: EditorBuffer,
  content: string,
): EditorBuffer {
  return { ...file, content, isDirty: content !== file.savedContent };
}

/** Acknowledge exactly what was written, without discarding subsequent typing. */
export function acknowledgeSavedContent(
  file: EditorBuffer,
  savedContent: string,
  revision?: string,
): EditorBuffer {
  return {
    ...file,
    savedContent,
    revision: revision ?? file.revision,
    isDirty: file.content !== savedContent,
  };
}

/** Best-effort stale-write preflight; atomic compare-and-swap belongs in the API. */
export function remoteMatchesBaseline(
  file: EditorBuffer,
  remoteContent: string,
): boolean {
  return remoteContent === file.savedContent;
}
