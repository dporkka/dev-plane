'use client';

import { useState } from 'react';
import { useParams } from 'next/navigation';
import { Card } from '@/components/ui/card';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Button } from '@/components/ui/button';
import Link from 'next/link';
import { ArrowLeft, Save } from 'lucide-react';

export default function ProjectSettingsPage() {
  const params = useParams();
  const projectId = params.id as string;
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');

  return (
    <div className="space-y-6 max-w-2xl">
      <div>
        <Link
          href={`/projects/${projectId}`}
          className="text-sm text-gray-500 hover:text-gray-300 flex items-center gap-1 mb-3"
        >
          <ArrowLeft className="w-4 h-4" />
          Back to Project
        </Link>
        <h1 className="text-2xl font-bold text-white">Project Settings</h1>
      </div>

      <Card>
        <div className="space-y-4">
          <div>
            <label className="block text-sm font-medium text-gray-300 mb-1">
              Project Name
            </label>
            <Input
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="my-project"
            />
          </div>
          <div>
            <label className="block text-sm font-medium text-gray-300 mb-1">
              Description
            </label>
            <Textarea
              value={description}
              onChange={(e) => setDescription(e.target.value)}
              placeholder="Describe your project..."
              rows={4}
            />
          </div>
          <div className="flex justify-end">
            <Button className="btn-primary flex items-center gap-2">
              <Save className="w-4 h-4" />
              Save Changes
            </Button>
          </div>
        </div>
      </Card>
    </div>
  );
}
