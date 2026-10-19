import { screen } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";

// Sign-out lives in the account menu in the top bar; open it first.
export async function openAccountMenu(user: UserEvent): Promise<void> {
  await user.click(
    await screen.findByRole("button", { name: /^Signed in as / }),
  );
}
