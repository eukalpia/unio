import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { MOCK_ACCESS_KEY, MOCK_LOGIN_LIMIT, MOCK_SECRET_KEY } from "../../mocks/state";
import { getSession } from "../../lib/session/store";
import { renderWithQuery } from "../../test/render";
import { LoginPage } from "./login-page";

const navigate = vi.fn<(options: unknown) => Promise<void>>(() => Promise.resolve());

vi.mock("@tanstack/react-router", () => ({
  useSearch: () => ({}),
  useNavigate: () => navigate,
}));

function fields() {
  return {
    accessKey: screen.getByLabelText("Access key"),
    secretKey: screen.getByLabelText<HTMLInputElement>("Secret key"),
    submit: screen.getByRole("button", { name: /sign in/i }),
  };
}

describe("LoginPage", () => {
  it("signs in, keeps the CSRF value in memory and clears the secret field", async () => {
    const user = userEvent.setup();
    renderWithQuery(<LoginPage />);
    const { accessKey, secretKey, submit } = fields();

    await user.type(accessKey, MOCK_ACCESS_KEY);
    await user.type(secretKey, MOCK_SECRET_KEY);
    await user.click(submit);

    await waitFor(() => {
      expect(getSession().status).toBe("signed-in");
    });
    const session = getSession();
    expect(session.status === "signed-in" && session.csrf).toMatch(/^csrf-/);
    expect(secretKey.value).toBe("");
    expect(JSON.stringify(session)).not.toContain(MOCK_SECRET_KEY);
    expect(navigate).toHaveBeenCalledWith({ href: "/" });
  });

  it("answers a wrong secret with one generic message and reveals nothing", async () => {
    const user = userEvent.setup();
    renderWithQuery(<LoginPage />);
    const { accessKey, secretKey, submit } = fields();

    await user.type(accessKey, MOCK_ACCESS_KEY);
    await user.type(secretKey, "not-the-secret");
    await user.click(submit);

    expect(await screen.findByText(/sign-in failed/i)).toBeInTheDocument();
    expect(screen.queryByText(/secret key is wrong|unknown access key|disabled/i)).toBeNull();
    expect(secretKey.value).toBe("");
    expect(getSession().status).toBe("unknown");
  });

  it("explains the rate limit and holds the button for a minute", async () => {
    const user = userEvent.setup();
    renderWithQuery(<LoginPage />);
    const { accessKey, secretKey, submit } = fields();

    for (let attempt = 0; attempt <= MOCK_LOGIN_LIMIT; attempt += 1) {
      await user.clear(accessKey);
      await user.type(accessKey, MOCK_ACCESS_KEY);
      await user.type(secretKey, "wrong");
      await user.click(submit);
      await waitFor(() => {
        expect(secretKey.value).toBe("");
      });
    }

    expect(await screen.findByText(/too many sign-in attempts/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /try again in \d+ s/i })).toBeDisabled();
  });

  it("asks for both fields without sending anything", async () => {
    const user = userEvent.setup();
    renderWithQuery(<LoginPage />);

    await user.click(fields().submit);

    expect(await screen.findByText("Enter both the access key and the secret key.")).toBeInTheDocument();
    expect(getSession().status).toBe("unknown");
  });
});
