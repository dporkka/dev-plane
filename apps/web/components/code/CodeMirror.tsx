'use client';

import React, { useMemo } from 'react';
import CodeMirror from '@uiw/react-codemirror';
import { vscodeDark } from '@uiw/codemirror-theme-vscode';
import { javascript } from '@codemirror/lang-javascript';
import { json } from '@codemirror/lang-json';
import { markdown } from '@codemirror/lang-markdown';

interface CodeEditorProps {
  value: string;
  language?: 'javascript' | 'typescript' | 'json' | 'markdown';
  readOnly?: boolean;
  onChange?: (value: string) => void;
  height?: string;
  className?: string;
}

const langMap = {
  javascript: () => javascript({ jsx: true, typescript: false }),
  typescript: () => javascript({ jsx: true, typescript: true }),
  json: () => json(),
  markdown: () => markdown(),
};

export function CodeEditor({
  value,
  language = 'typescript',
  readOnly = false,
  onChange,
  height = '400px',
  className,
}: CodeEditorProps) {
  const extensions = useMemo(() => [langMap[language]()], [language]);

  return (
    <CodeMirror
      value={value}
      height={height}
      theme={vscodeDark}
      extensions={extensions}
      readOnly={readOnly}
      onChange={onChange}
      className={`rounded-md border border-[#30363d] overflow-hidden text-sm ${className || ''}`}
      basicSetup={{
        lineNumbers: true,
        highlightActiveLineGutter: true,
        highlightActiveLine: true,
        foldGutter: true,
      }}
    />
  );
}
