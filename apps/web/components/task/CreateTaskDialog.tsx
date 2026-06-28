'use client';

import { type FormEvent, useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { api } from '@/lib/api';
import { Button } from '@/components/ui/button';
import { Dialog } from '@/components/ui/dialog';
import { Input } from '@/components/ui/input';
import { Select } from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import { useToast } from '@/components/ui/toast';
import type { Priority, Repository, RiskLevel } from '@/lib/types';

interface CreateTaskDialogProps {
  projectId: string;
  repos: Repository[];
  open: boolean;
  onClose: () => void;
}

const priorityOptions: { value: Priority; label: string }[] = [
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'urgent', label: 'Urgent' },
];

const riskOptions: { value: RiskLevel; label: string }[] = [
  { value: 'low', label: 'Low' },
  { value: 'medium', label: 'Medium' },
  { value: 'high', label: 'High' },
  { value: 'critical', label: 'Critical' },
];

export function CreateTaskDialog({
  projectId,
  repos,
  open,
  onClose,
}: CreateTaskDialogProps) {
  const queryClient = useQueryClient();
  const { addToast } = useToast();
  const [title, setTitle] = useState('');
  const [description, setDescription] = useState('');
  const [repositoryId, setRepositoryId] = useState('');
  const [priority, setPriority] = useState<Priority>('medium');
  const [riskLevel, setRiskLevel] = useState<RiskLevel>('low');
  const [targetBranch, setTargetBranch] = useState('main');
  const [maxCost, setMaxCost] = useState('');
  const [formError, setFormError] = useState<string | null>(null);

  const createTask = useMutation({
    mutationFn: () =>
      api.createTask(projectId, {
        repository_id: repositoryId,
        title: title.trim(),
        description: description.trim(),
        priority,
        risk_level: riskLevel,
        target_branch: targetBranch.trim() || 'main',
        max_cost: maxCost ? Number(maxCost) : undefined,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['tasks', projectId] });
      addToast('Task created successfully', 'success');
      reset();
      onClose();
    },
    onError: (error) => {
      setFormError(error instanceof Error ? error.message : 'Failed to create task.');
      addToast('Failed to create task', 'error');
    },
  });

  function reset() {
    setTitle('');
    setDescription('');
    setRepositoryId('');
    setPriority('medium');
    setRiskLevel('low');
    setTargetBranch('main');
    setMaxCost('');
    setFormError(null);
  }

  function handleClose() {
    if (createTask.isPending) return;
    reset();
    onClose();
  }

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!title.trim()) {
      setFormError('Title is required.');
      return;
    }
    if (!repositoryId) {
      setFormError('Select a repository for this task.');
      return;
    }
    setFormError(null);
    createTask.mutate();
  }

  const repoOptions = repos.map((repo) => ({
    value: repo.id,
    label: repo.full_name,
  }));

  return (
    <Dialog open={open} onClose={handleClose} title="New Task">
      <form onSubmit={handleSubmit} className="space-y-4">
        <div className="space-y-2">
          <label htmlFor="task-title" className="block text-sm font-medium text-gray-300">
            Title
          </label>
          <Input
            id="task-title"
            value={title}
            onChange={(event) => setTitle(event.target.value)}
            placeholder="Implement checkout flow"
            autoFocus
            disabled={createTask.isPending}
          />
        </div>

        <div className="space-y-2">
          <label htmlFor="task-repo" className="block text-sm font-medium text-gray-300">
            Repository
          </label>
          <Select
            id="task-repo"
            value={repositoryId}
            onChange={(event) => setRepositoryId(event.target.value)}
            disabled={createTask.isPending || repos.length === 0}
            options={[
              { value: '', label: repos.length === 0 ? 'No repositories connected' : 'Select a repository' },
              ...repoOptions,
            ]}
          />
        </div>

        <div className="space-y-2">
          <label htmlFor="task-description" className="block text-sm font-medium text-gray-300">
            Description
          </label>
          <Textarea
            id="task-description"
            value={description}
            onChange={(event) => setDescription(event.target.value)}
            placeholder="What should the agent build?"
            disabled={createTask.isPending}
          />
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <label htmlFor="task-priority" className="block text-sm font-medium text-gray-300">
              Priority
            </label>
            <Select
              id="task-priority"
              value={priority}
              onChange={(event) => setPriority(event.target.value as Priority)}
              disabled={createTask.isPending}
              options={priorityOptions}
            />
          </div>

          <div className="space-y-2">
            <label htmlFor="task-risk" className="block text-sm font-medium text-gray-300">
              Risk level
            </label>
            <Select
              id="task-risk"
              value={riskLevel}
              onChange={(event) => setRiskLevel(event.target.value as RiskLevel)}
              disabled={createTask.isPending}
              options={riskOptions}
            />
          </div>
        </div>

        <div className="grid grid-cols-2 gap-4">
          <div className="space-y-2">
            <label htmlFor="task-branch" className="block text-sm font-medium text-gray-300">
              Target branch
            </label>
            <Input
              id="task-branch"
              value={targetBranch}
              onChange={(event) => setTargetBranch(event.target.value)}
              placeholder="main"
              disabled={createTask.isPending}
            />
          </div>

          <div className="space-y-2">
            <label htmlFor="task-cost" className="block text-sm font-medium text-gray-300">
              Max cost (USD)
            </label>
            <Input
              id="task-cost"
              type="number"
              min={0}
              step={0.01}
              value={maxCost}
              onChange={(event) => setMaxCost(event.target.value)}
              placeholder="Optional"
              disabled={createTask.isPending}
            />
          </div>
        </div>

        {formError && (
          <div className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-300">
            {formError}
          </div>
        )}

        <div className="flex justify-end gap-2 pt-2">
          <Button type="button" variant="secondary" onClick={handleClose} disabled={createTask.isPending}>
            Cancel
          </Button>
          <Button type="submit" disabled={createTask.isPending || repos.length === 0}>
            {createTask.isPending ? 'Creating...' : 'Create Task'}
          </Button>
        </div>
      </form>
    </Dialog>
  );
}
