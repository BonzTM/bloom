import { expect, it, jest } from "@jest/globals";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiError } from "../../../lib/api/errors.js";
import { LoginForm } from "./login-form.js";

function renderForm(overrides: Partial<Parameters<typeof LoginForm>[0]> = {}): {
  onSubmit: jest.Mock;
} {
  const onSubmit = jest.fn();
  render(
    <LoginForm
      pending={false}
      serverError={null}
      onSubmit={onSubmit}
      {...overrides}
    />,
  );
  return { onSubmit };
}

it("submits trimmed credentials when both fields are filled", async () => {
  const user = userEvent.setup();
  const { onSubmit } = renderForm();

  await user.type(screen.getByLabelText("Username"), "  admin ");
  await user.type(screen.getByLabelText("Password"), "correct horse");
  await user.click(screen.getByRole("button", { name: "Sign in" }));

  expect(onSubmit).toHaveBeenCalledWith({
    username: "admin",
    password: "correct horse",
  });
});

it("shows field errors, focuses the first invalid field, and keeps input", async () => {
  const user = userEvent.setup();
  const { onSubmit } = renderForm();

  await user.type(screen.getByLabelText("Password"), "secret");
  await user.click(screen.getByRole("button", { name: "Sign in" }));

  const username = screen.getByLabelText("Username");
  expect(username).toHaveAttribute("aria-invalid", "true");
  expect(username).toHaveAccessibleDescription("Enter your username");
  expect(username).toHaveFocus();
  expect(screen.getByLabelText("Password")).toHaveValue("secret");
  expect(onSubmit).not.toHaveBeenCalled();
});

it("does not submit while a previous attempt is pending", async () => {
  const user = userEvent.setup();
  const { onSubmit } = renderForm({ pending: true });

  const button = screen.getByRole("button", { name: "Signing in…" });
  expect(button).toBeDisabled();
  await user.click(button);

  expect(onSubmit).not.toHaveBeenCalled();
});

it("explains a rejected sign-in without saying which field was wrong", () => {
  renderForm({
    serverError: new ApiError("http", "invalid credentials", { status: 401 }),
  });

  expect(screen.getByRole("alert")).toHaveTextContent(
    "The username or password is incorrect.",
  );
});

it("tells the person how long to wait when rate limited", () => {
  renderForm({
    serverError: new ApiError("http", "too many", {
      status: 429,
      retryAfterSeconds: 30,
    }),
  });

  expect(screen.getByRole("alert")).toHaveTextContent(
    "Too many sign-in attempts. Try again in 30 seconds.",
  );
});

it("gives a connection hint for network failures", () => {
  renderForm({
    serverError: new ApiError("network", "The server could not be reached"),
  });

  expect(screen.getByRole("alert")).toHaveTextContent(
    "The server could not be reached.",
  );
});
