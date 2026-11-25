import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { http, HttpResponse } from "msw";
import { API_BASE } from "../../lib/api/client";
import { api } from "../../lib/api/instance";
import { MOCK_ACCESS_KEY, MOCK_SECRET_KEY } from "../../mocks/state";
import { server } from "../../mocks/server";
import { renderWithQuery } from "../../test/render";
import { PoliciesPage } from "./policies-page";

describe("PoliciesPage", () => {
  it("shows a clear not-available state while the node answers 501", async () => {
    // The node checks the session first, then answers 501 for the policy API until the IAM change lands.
    await api.login(MOCK_ACCESS_KEY, MOCK_SECRET_KEY);
    renderWithQuery(<PoliciesPage />);

    expect(await screen.findByText("Named policies are not available on this node yet")).toBeInTheDocument();
    expect(screen.queryByText(/could not be loaded/i)).toBeNull();
    expect(screen.getByRole("button", { name: "New policy" })).toBeDisabled();
  });

  it("lists policies and opens the editor once the node implements the API", async () => {
    server.use(
      http.get(`${API_BASE}policies`, () =>
        HttpResponse.json([{ name: "ReadOnly", document: { Statement: [{ Effect: "Allow" }] } }]),
      ),
    );
    renderWithQuery(<PoliciesPage />);

    const entry = await screen.findByRole("button", { name: "ReadOnly" });
    await userEvent.setup().click(entry);

    const editor = await screen.findByLabelText<HTMLTextAreaElement>("Policy document (JSON)");
    expect(editor.value).toContain('"Statement"');
    expect(screen.getByRole("button", { name: "Save policy" })).toBeEnabled();
  });

  it("reports other failures as errors with a retry", async () => {
    server.use(
      http.get(`${API_BASE}policies`, () =>
        HttpResponse.json({ code: "InternalError", message: "Something went wrong.", requestId: "r" }, { status: 500 }),
      ),
    );
    renderWithQuery(<PoliciesPage />);

    expect(await screen.findByText("Policies could not be loaded")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
  });
});
