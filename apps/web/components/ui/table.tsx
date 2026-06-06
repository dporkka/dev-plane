import React from 'react';
import { cn } from '@/lib/utils';

interface TableProps extends React.TableHTMLAttributes<HTMLTableElement> {
  children: React.ReactNode;
}

export function Table({ className, children, ...props }: TableProps) {
  return (
    <div className="overflow-auto">
      <table
        className={cn('w-full text-sm text-left', className)}
        {...props}
      >
        {children}
      </table>
    </div>
  );
}

export function TableHead({ className, children, ...props }: React.HTMLAttributes<HTMLTableSectionElement>) {
  return (
    <thead
      className={cn('text-xs text-gray-400 uppercase bg-[#161b22]', className)}
      {...props}
    >
      {children}
    </thead>
  );
}

export function TableBody({ className, children, ...props }: React.HTMLAttributes<HTMLTableSectionElement>) {
  return (
    <tbody className={cn('divide-y divide-[#30363d]', className)} {...props}>
      {children}
    </tbody>
  );
}

export function TableRow({ className, children, ...props }: React.HTMLAttributes<HTMLTableRowElement>) {
  return (
    <tr
      className={cn('hover:bg-[#161b22]/80 transition-colors', className)}
      {...props}
    >
      {children}
    </tr>
  );
}

export function TableHeader({ className, children, ...props }: React.ThHTMLAttributes<HTMLTableHeaderCellElement>) {
  return (
    <th
      className={cn('px-4 py-3 font-medium', className)}
      {...props}
    >
      {children}
    </th>
  );
}

export function TableCell({ className, children, ...props }: React.TdHTMLAttributes<HTMLTableDataCellElement>) {
  return (
    <td
      className={cn('px-4 py-3 text-gray-300', className)}
      {...props}
    >
      {children}
    </td>
  );
}
