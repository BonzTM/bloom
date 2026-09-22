import { Component, type ErrorInfo, type ReactNode } from "react";

type Props = Readonly<{ children: ReactNode }>;
type State = Readonly<{ failed: boolean }>;

export class RouteErrorBoundary extends Component<Props, State> {
  override state: State = { failed: false };

  static getDerivedStateFromError(): State {
    return { failed: true };
  }

  override componentDidCatch(error: Error, info: ErrorInfo): void {
    console.error("A route failed to render", {
      error,
      componentStack: info.componentStack,
    });
  }

  override render(): ReactNode {
    if (this.state.failed) {
      return <p role="alert">This page could not be displayed.</p>;
    }
    return this.props.children;
  }
}
