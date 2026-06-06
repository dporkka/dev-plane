'use client';

import React from 'react';
import { cn } from '@/lib/utils';
import { DollarSign } from 'lucide-react';

interface CostBadgeProps {
  cost: number;
  className?: string;
}

export function CostBadge({ cost, className }: CostBadgeProps) {
  const formatted = cost < 0.01 ? '<$0.01' : `$${cost.toFixed(2)}`;

  return (
    <span
      className={cn(
        'inline-flex items-center gap-1 text-xs text-gray-500',
        className
      )}
    >
      <DollarSign className="w-3 h-3" />
      {formatted}
    </span>
  );
}
