import React from 'react';
import { cn } from '@/lib/utils';

interface SelectOption {
  value: string;
  label: string;
}

interface SelectProps extends React.SelectHTMLAttributes<HTMLSelectElement> {
  options: SelectOption[];
}

export const Select = React.forwardRef<HTMLSelectElement, SelectProps>(
  ({ className, options, ...props }, ref) => {
    return (
      <select
        ref={ref}
        className={cn(
          'px-3 py-2 bg-[#0d1117] border border-[#30363d] rounded-md',
          'text-gray-100',
          'focus:outline-none focus:ring-2 focus:ring-blue-500/50 focus:border-blue-500',
          'disabled:opacity-50 disabled:cursor-not-allowed',
          'w-full',
          className
        )}
        {...props}
      >
        {options.map((option) => (
          <option key={option.value} value={option.value} className="bg-[#0d1117]">
            {option.label}
          </option>
        ))}
      </select>
    );
  }
);
Select.displayName = 'Select';
