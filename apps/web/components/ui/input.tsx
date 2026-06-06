import React from 'react';
import { cn } from '@/lib/utils';

interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {}

export const Input = React.forwardRef<HTMLInputElement, InputProps>(
  ({ className, ...props }, ref) => {
    return (
      <input
        ref={ref}
        className={cn(
          'px-3 py-2 bg-[#0d1117] border border-[#30363d] rounded-md',
          'text-gray-100 placeholder-gray-500',
          'focus:outline-none focus:ring-2 focus:ring-blue-500/50 focus:border-blue-500',
          'disabled:opacity-50 disabled:cursor-not-allowed',
          'w-full',
          className
        )}
        {...props}
      />
    );
  }
);
Input.displayName = 'Input';
