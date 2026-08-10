import { describe, expect, it, vi } from "vitest";

import { hostOriginFromHash, NAVIGATE_MESSAGE_TYPE, postNavigate, sharesRoute } from "./navigate";

describe("hostOriginFromHash", () => {
  it("reads the origin the bridge pinned", () => {
    expect(hostOriginFromHash("#lattice_nonce=abc&host_origin=https%3A%2F%2Fconsole.example.com")).toBe(
      "https://console.example.com",
    );
  });

  it("is fail-closed: anything odd is a reason to stay silent", () => {
    expect(hostOriginFromHash("")).toBeNull();
    expect(hostOriginFromHash("#lattice_nonce=abc")).toBeNull();
    expect(hostOriginFromHash("#host_origin=not-a-url")).toBeNull();
    expect(hostOriginFromHash("#host_origin=javascript%3Aalert(1)")).toBeNull();
    expect(hostOriginFromHash("#host_origin=ftp%3A%2F%2Fexample.com")).toBeNull();
  });

  it("reduces a URL to its origin, ignoring any path", () => {
    expect(hostOriginFromHash("#host_origin=https%3A%2F%2Fconsole.example.com%2Fsome%2Fpath")).toBe(
      "https://console.example.com",
    );
  });
});

describe("sharesRoute", () => {
  it("points at the Shares view with the create form open for the record", () => {
    expect(sharesRoute("Home nodes")).toBe("/network/subscription-shares?create=1&for=Home%20nodes");
  });
});

describe("postNavigate", () => {
  it("posts exactly the contract shape to the parent at the pinned origin", () => {
    const postMessage = vi.fn();
    const win = { parent: { postMessage } } as unknown as Window;
    postNavigate(win, sharesRoute("Home nodes"), "https://console.example.com");
    expect(postMessage).toHaveBeenCalledWith(
      {
        type: NAVIGATE_MESSAGE_TYPE,
        route: "/network/subscription-shares?create=1&for=Home%20nodes",
      },
      "https://console.example.com",
    );
    expect(NAVIGATE_MESSAGE_TYPE).toBe("lattice:navigate");
  });
});
