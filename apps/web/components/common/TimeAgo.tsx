"use client";
import { cn } from "@/lib/utils";
import { formatDistanceToNow, parseISO } from "date-fns";

interface TimeAgoProps {
  date?: string | Date;
  className?: string;
}

export function TimeAgo({ date, className }: TimeAgoProps) {
  if (!date) return <span className={cn("text-gray-500", className)}>—</span>;

  try {
    const parsed = typeof date === "string" ? parseISO(date) : date;
    const distance = formatDistanceToNow(parsed, { addSuffix: true });

    return (
      <span
        className={cn("text-gray-500", className)}
        title={parsed.toISOString()}
      >
        {distance}
      </span>
    );
  } catch {
    return <span className={cn("text-gray-500", className)}>—</span>;
  }
}
