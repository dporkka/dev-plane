'use client';

import React, { useState } from 'react';
import { CodeEditor } from '@/components/code/CodeMirror';
import { Card } from '@/components/ui/card';
import { CheckCircle, XCircle, Edit3, Save } from 'lucide-react';

interface SpecEditorProps {
  spec: string;
  taskId: string;
  onApprove?: () => void;
  onReject?: () => void;
  readOnly?: boolean;
}

export function SpecEditor({
  spec,
  onApprove,
  onReject,
  readOnly = false,
}: SpecEditorProps) {
  const [isEditing, setIsEditing] = useState(false);
  const [editedSpec, setEditedSpec] = useState(spec);

  return (
    <div className="space-y-4">
      <Card className="p-0 overflow-hidden">
        <div className="flex items-center justify-between px-4 py-3 border-b border-[#30363d] bg-[#161b22]">
          <span className="text-sm text-gray-400 font-medium">Generated Specification</span>
          {!readOnly && (
            <button
              onClick={() => {
                if (isEditing) {
                  setIsEditing(false);
                } else {
                  setEditedSpec(spec);
                  setIsEditing(true);
                }
              }}
              className="btn-secondary text-xs flex items-center gap-1"
            >
              {isEditing ? (
                <>
                  <Save className="w-3 h-3" />
                  Done
                </>
              ) : (
                <>
                  <Edit3 className="w-3 h-3" />
                  Edit
                </>
              )}
            </button>
          )}
        </div>
        <CodeEditor
          value={isEditing ? editedSpec : spec}
          language="markdown"
          readOnly={!isEditing}
          onChange={isEditing ? setEditedSpec : undefined}
          height="500px"
        />
      </Card>

      {!readOnly && (
        <div className="flex items-center gap-3">
          <button
            onClick={onApprove}
            className="btn-primary flex items-center gap-2"
          >
            <CheckCircle className="w-4 h-4" />
            Approve Spec
          </button>
          <button
            onClick={onReject}
            className="btn-secondary flex items-center gap-2 text-red-400 border-red-500/30 hover:bg-red-500/10"
          >
            <XCircle className="w-4 h-4" />
            Request Changes
          </button>
        </div>
      )}
    </div>
  );
}
