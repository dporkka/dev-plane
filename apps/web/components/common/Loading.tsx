'use client';

import React from 'react';
import { SkeletonCard, SkeletonStats } from '@/components/ui/skeleton';
import { Loader2 } from 'lucide-react';
import { cn } from '@/lib/utils';

interface LoadingProps {
  className?: string;
  text?: string;
}

export function Loading({ className, text = 'Loading...' }: LoadingProps) {
  return (
    <div className={cn('flex items-center justify-center py-12', className)}>
      <Loader2 className="w-6 h-6 text-blue-400 animate-spin mr-3" />
      <span className="text-gray-400">{text}</span>
    </div>
  );
}

export function LoadingPage() {
  return (
    <div className="space-y-6">
      <SkeletonStats />
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        <div className="space-y-2">
          {[1, 2, 3].map((i) => (
            <SkeletonCard key={i} />
          ))}
        </div>
        <div className="space-y-2">
          {[1, 2, 3].map((i) => (
            <SkeletonCard key={i} />
          ))}
        </div>
      </div>
    </div>
  );
}

export function LoadingSpinner({ className }: { className?: string }) {
  return (
    <Loader2 className={cn('w-5 h-5 text-blue-400 animate-spin', className)} />
  );
}
