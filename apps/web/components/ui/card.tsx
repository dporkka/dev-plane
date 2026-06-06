import React from 'react';
import { cn } from '@/lib/utils';

interface CardProps extends React.HTMLAttributes<HTMLDivElement> {
  children: React.ReactNode;
}

export function Card({ className, children, ...props }: CardProps) {
  return (
    <div
      className={cn(
        'rounded-lg border border-[#30363d] bg-[#161b22]/50 p-4',
        className
      )}
      {...props}
    >
      {children}
    </div>
  );
}
