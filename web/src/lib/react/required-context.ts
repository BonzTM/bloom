import { createContext, useContext, type Context } from "react";

type RequiredContext<T> = Readonly<{
  Provider: Context<T | undefined>;
  useValue: () => T;
}>;

// A context whose consumers may assume a value is present. Reading it outside
// its provider is a wiring bug, so it throws with the dependency's name.
export function createRequiredContext<T>(name: string): RequiredContext<T> {
  const context = createContext<T | undefined>(undefined);
  context.displayName = name;
  function useValue(): T {
    const value = useContext(context);
    if (value === undefined) {
      throw new Error(`${name} provider is missing`);
    }
    return value;
  }
  return { Provider: context, useValue };
}
