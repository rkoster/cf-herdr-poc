import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import { CreateSandboxForm } from "@/components/create-sandbox-form";

test("serializes create submissions while pending", async () => {
  const user = userEvent.setup();
  let resolve!: (created: boolean) => void;
  const pending = new Promise<boolean>((accept) => { resolve = accept; });
  const onCreate = vi.fn(() => pending);
  render(<CreateSandboxForm buildpacks={["ruby_buildpack"]} onCreate={onCreate} />);
  await user.type(screen.getByLabelText("Name"), "new-app");
  await user.type(screen.getByLabelText("Git repository"), "https://git.example/new.git");
  const create = screen.getByRole("button", { name: "Create sandbox" });
  await user.dblClick(create);
  expect(onCreate).toHaveBeenCalledTimes(1);
  expect(screen.getByRole("button", { name: "Creating..." })).toBeDisabled();
  await act(async () => resolve(false));
  expect(screen.getByRole("button", { name: "Create sandbox" })).toBeEnabled();
});
