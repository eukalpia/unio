import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { api } from "../../lib/api/instance";
import { setSession } from "../../lib/session/store";
import { MOCK_ACCESS_KEY, MOCK_SECRET_KEY, mockState } from "../../mocks/state";
import { renderWithQuery } from "../../test/render";
import { CreateBucketDialog } from "./create-bucket-dialog";

/** Signs in through the contract mock and stores the session the way the sign-in page does. */
async function signIn(): Promise<void> {
  const created = await api.login(MOCK_ACCESS_KEY, MOCK_SECRET_KEY);
  setSession({ status: "signed-in", accessKey: created.accessKey, expiresAt: created.expiresAt, csrf: created.csrf });
}

describe("CreateBucketDialog", () => {
  it("explains a broken name and sends nothing", async () => {
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    renderWithQuery(<CreateBucketDialog open onOpenChange={onOpenChange} existing={[]} />);

    await user.type(screen.getByLabelText("Bucket name"), "My_Bucket");
    await user.click(screen.getByRole("button", { name: "Create bucket" }));

    expect(await screen.findByText("Use only lowercase letters, numbers, dots (.) and hyphens (-).")).toBeInTheDocument();
    expect(mockState().buckets.has("My_Bucket")).toBe(false);
    expect(onOpenChange).not.toHaveBeenCalledWith(false);
  });

  it("creates a valid bucket with the CSRF value and closes", async () => {
    await signIn();
    const user = userEvent.setup();
    const onOpenChange = vi.fn();
    renderWithQuery(<CreateBucketDialog open onOpenChange={onOpenChange} existing={[]} />);

    await user.type(screen.getByLabelText("Bucket name"), "photos-2026");
    await user.click(screen.getByRole("button", { name: "Create bucket" }));

    await waitFor(() => {
      expect(mockState().buckets.has("photos-2026")).toBe(true);
    });
    await waitFor(() => {
      expect(onOpenChange).toHaveBeenCalledWith(false);
    });
  });

  it("shows the server's refusal when a name passes the client rules", async () => {
    await signIn();
    const user = userEvent.setup();
    renderWithQuery(<CreateBucketDialog open onOpenChange={vi.fn()} existing={[]} />);

    await user.type(screen.getByLabelText("Bucket name"), "backups");
    await user.click(screen.getByRole("button", { name: "Create bucket" }));

    expect(await screen.findByText("A bucket with this name already exists.")).toBeInTheDocument();
  });

  it("asks for a fresh sign-in instead of sending a mutation without CSRF", async () => {
    const user = userEvent.setup();
    renderWithQuery(<CreateBucketDialog open onOpenChange={vi.fn()} existing={[]} />);

    await user.type(screen.getByLabelText("Bucket name"), "logs-archive");
    await user.click(screen.getByRole("button", { name: "Create bucket" }));

    expect(await screen.findByText("Sign in again to make changes.")).toBeInTheDocument();
    expect(mockState().buckets.has("logs-archive")).toBe(false);
  });
});
