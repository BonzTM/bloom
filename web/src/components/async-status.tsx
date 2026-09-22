import type { ReactNode } from "react";

type AsyncStatusProps = Readonly<{
  children: ReactNode;
  kind?: "alert" | "status";
}>;

export function AsyncStatus({
  children,
  kind = "status",
}: AsyncStatusProps): ReactNode {
  return <p role={kind}>{children}</p>;
}
