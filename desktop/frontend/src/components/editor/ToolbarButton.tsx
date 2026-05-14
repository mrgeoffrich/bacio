import React from 'react';

interface ToolbarButtonProps {
  onClick: () => void;
  title: string;
  active?: boolean;
  disabled?: boolean;
  children: React.ReactNode;
}

// Plain replacement for claudette's shadcn <Button variant="ghost">. The
// toolbar/bubble-menu CSS styles `button` and `button[data-active="true"]`
// directly, so this just needs to forward the data-active flag.
export function ToolbarButton({ onClick, title, active, disabled, children }: ToolbarButtonProps) {
  return (
    <button
      type="button"
      className="toolbar-button"
      onClick={onClick}
      data-active={active ? 'true' : undefined}
      disabled={disabled}
      title={title}
    >
      {children}
    </button>
  );
}
