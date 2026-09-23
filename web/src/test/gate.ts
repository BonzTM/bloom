// A promise the test opens by hand, so a "slow" response is held exactly as
// long as the test needs and never depends on the wall clock.
export type Gate = Readonly<{ wait: Promise<void>; open: () => void }>;

export function createGate(): Gate {
  let open = (): void => undefined;
  const wait = new Promise<void>((resolve) => {
    open = resolve;
  });
  return { wait, open };
}
