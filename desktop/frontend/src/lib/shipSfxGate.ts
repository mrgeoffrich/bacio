// Pure decision function for BACI-240 ship-SFX gating. Lives in a
// sibling module so the Node smoketest can import it without
// pulling shipSfx.ts (which imports the asset URL via Vite — a
// runtime path Node doesn't know how to resolve).
//
// useShipSfx in shipSfx.ts re-exports this and is the integration
// point; this file is the unit-testable core.

// shouldPlayShipSfx returns true only when every gate passes:
//   - the user enabled the toggle;
//   - the Audio constructor is reachable (a non-browser env / locked
//     down profile fails here).
//
// BACI-295: `prefers-reduced-motion` is deliberately NOT a gate. That
// preference governs animation, not audio — the visual ship flight +
// odometer roll still honour it, but a user who opted into the ship
// sound hears it regardless of their motion preference. (The browser's
// autoplay policy is still the real silencer when no gesture has
// landed; that's handled downstream in shipSfx.ts, not here.)
export function shouldPlayShipSfx(
  enabled: boolean,
  audioCtor: unknown,
): boolean {
  if (!enabled) return false;
  if (!audioCtor) return false;
  return true;
}
