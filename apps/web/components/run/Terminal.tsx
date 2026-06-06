'use client';

import React, { useEffect, useRef } from 'react';
import { Terminal as XTerm } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';

interface TerminalProps {
  logs: string[];
  height?: string;
}

export function Terminal({ logs, height = '400px' }: TerminalProps) {
  const terminalRef = useRef<HTMLDivElement>(null);
  const xtermRef = useRef<XTerm | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const logsRef = useRef<string[]>([]);

  useEffect(() => {
    if (!terminalRef.current) return;

    const term = new XTerm({
      theme: {
        background: '#0d1117',
        foreground: '#c9d1d9',
        cursor: '#c9d1d9',
        selectionBackground: '#264f78',
        black: '#0d1117',
        red: '#f85149',
        green: '#3fb950',
        yellow: '#d29922',
        blue: '#58a6ff',
        magenta: '#bc8cff',
        cyan: '#39c5cf',
        white: '#c9d1d9',
      },
      fontSize: 13,
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
      scrollback: 10000,
      cursorBlink: true,
    });

    const fitAddon = new FitAddon();
    term.loadAddon(fitAddon);
    term.open(terminalRef.current);

    // Small delay to ensure container has size
    setTimeout(() => {
      fitAddon.fit();
    }, 100);

    xtermRef.current = term;
    fitAddonRef.current = fitAddon;

    const handleResize = () => fitAddon.fit();
    window.addEventListener('resize', handleResize);

    return () => {
      window.removeEventListener('resize', handleResize);
      term.dispose();
    };
  }, []);

  useEffect(() => {
    if (xtermRef.current && logs.length > logsRef.current.length) {
      const newLogs = logs.slice(logsRef.current.length);
      newLogs.forEach((line) => xtermRef.current?.writeln(line));
      logsRef.current = logs;
    }
  }, [logs]);

  return (
    <div
      ref={terminalRef}
      style={{ height, width: '100%' }}
      className="rounded-md border border-[#30363d] overflow-hidden"
    />
  );
}
