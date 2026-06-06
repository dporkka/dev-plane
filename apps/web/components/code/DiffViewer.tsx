'use client';

import React from 'react';
import { diffLines } from 'diff';

interface DiffViewerProps {
  oldValue: string;
  newValue: string;
  filename?: string;
}

export function DiffViewer({ oldValue, newValue, filename }: DiffViewerProps) {
  const diff = diffLines(oldValue, newValue);

  return (
    <div className="rounded-md border border-[#30363d] bg-[#0d1117] overflow-hidden">
      {filename && (
        <div className="px-4 py-2 bg-[#161b22] border-b border-[#30363d] text-sm font-medium text-gray-300">
          {filename}
        </div>
      )}
      <div className="overflow-auto max-h-[600px]">
        <pre className="text-sm leading-5">
          {diff.map((part, i) => (
            <div
              key={i}
              className={`px-4 ${
                part.added
                  ? 'bg-green-900/20 text-green-300'
                  : part.removed
                  ? 'bg-red-900/20 text-red-300'
                  : 'text-gray-300'
              }`}
            >
              {part.value.split('\n').map(
                (line, j) =>
                  j < part.value.split('\n').length - 1 && (
                    <div key={j} className="flex">
                      <span className="select-none w-6 text-gray-600 text-right mr-3 flex-shrink-0">
                        {part.added ? '+' : part.removed ? '-' : ' '}
                      </span>
                      <span className="break-all">{line}</span>
                    </div>
                  )
              )}
            </div>
          ))}
        </pre>
      </div>
    </div>
  );
}
