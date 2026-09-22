import { expect, it, jest } from "@jest/globals";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { ApiError } from "../../../lib/api/errors.js";
import { LoginForm } from "./login-form.js";

type Props = Parameters<typeof LoginForm>[0];

function renderForm(overrides: Partial<Props> = {}): {
  onSubmit: jest.Mock;
  rerender: (overrides: Partial<Props>) => void;
} {
  const onSubmit = jest.fn();
  const props = (extra: Partial<Props>): Props => ({
    pending: false,
    serverError: null,
    onSubmit,
    ...overrides,
    ...extra,
  });
  const view = render(<LoginForm {...props({})} />);
  return {
    onSubmit,
    rerender: (extra) => {
      view.rerender(<LoginForm {...props(extra)} />);
    },
  };
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

it("announces the pending state and blocks a second submit", async () => {
  const user = userEvent.setup();
  const { onSubmit } = renderForm({ pending: true });

  const button = screen.getByRole("button", { name: "Signing in…" });
  expect(button).toBeDisabled();
  expect(screen.getByRole("form", { name: "Sign in" })).toHaveAttribute(
    "aria-busy",
    "true",
  );
  expect(screen.getByRole("status")).toHaveTextContent(
    "Signing in, please wait.",
  );
  await user.click(button);

  expect(onSubmit).not.toHaveBeenCalled();
});

it("focuses the explanation and keeps the typed input after a rejection", async () => {
  const user = userEvent.setup();
  const { rerender } = renderForm();
  await user.type(screen.getByLabelText("Username"), "admin");
  await user.type(screen.getByLabelText("Password"), "wrong");

  rerender({
    serverError: new ApiError("http", "invalid credentials", { status: 401 }),
  });

  const alert = screen.getByRole("alert");
  expect(alert).toHaveTextContent("The username or password is incorrect.");
  expect(alert).toHaveFocus();
  expect(screen.getByLabelText("Username")).toHaveValue("admin");
  expect(screen.getByLabelText("Password")).toHaveValue("wrong");
});

it("does not describe the form by a summary that is not rendered", () => {
  renderForm();

  expect(screen.getByRole("form", { name: "Sign in" })).not.toHaveAttribute(
    "aria-describedby",
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
