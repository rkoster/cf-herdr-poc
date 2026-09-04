import type { RouteObject } from "react-router";
import { createBrowserRouter } from "react-router";

import { RootError, RootRoute, rootLoader } from "@/routes/root";
import { SandboxesRoute } from "@/routes/sandboxes";

export const routes: RouteObject[] = [{ path: "/", loader: rootLoader, hydrateFallbackElement: null, element: <RootRoute />, errorElement: <RootError />, children: [{ index: true, element: <SandboxesRoute /> }] }];
export const router = createBrowserRouter(routes, { basename: "/manager" });
