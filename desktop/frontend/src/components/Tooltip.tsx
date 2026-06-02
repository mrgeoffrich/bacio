import type React from 'react';
import * as RTooltip from '@radix-ui/react-tooltip';

type TooltipProps = {
  label?: React.ReactNode;
  side?: RTooltip.TooltipContentProps['side'];
  align?: RTooltip.TooltipContentProps['align'];
  children: React.ReactElement;
  asChild?: boolean;
};

// Thin wrapper around Radix Tooltip with the bacio mk-* defaults baked
// in: 250ms open delay, portal to root so the bubble escapes overflow
// clipping, 4px gap from the trigger. Wrap a single trigger element:
//
//   <Tooltip label="What this does">
//     <button>Click</button>
//   </Tooltip>
//
// Pass `side` / `align` to override placement (defaults to top + centre).
export default function Tooltip({ label, side = 'top', align = 'center', children, asChild = true }: TooltipProps) {
  if (!label) return children;
  return (
    <RTooltip.Root>
      <RTooltip.Trigger asChild={asChild}>{children}</RTooltip.Trigger>
      <RTooltip.Portal>
        <RTooltip.Content
          className="mk-tooltip"
          side={side}
          align={align}
          sideOffset={4}
          collisionPadding={8}
        >
          {label}
          <RTooltip.Arrow className="mk-tooltip-arrow" />
        </RTooltip.Content>
      </RTooltip.Portal>
    </RTooltip.Root>
  );
}
