import { describe, it, expect } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import App from "./App";

describe("App", () => {
  it("renders the consistent hashing page by default", () => {
    render(<App />);
    expect(screen.getByRole("heading", { name: "Consistent Hashing" })).toBeTruthy();
  });

  it("switches pages via the sidebar", () => {
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: "Rate Limiting" }));
    expect(screen.getByRole("heading", { name: "Rate Limiting" })).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Circuit Breakers" }));
    expect(screen.getByRole("heading", { name: "Circuit Breakers" })).toBeTruthy();
  });
});

describe("ConsistentHashingViz", () => {
  it("adding a node increases the node count shown", () => {
    render(<App />);
    const before = screen.getAllByRole("button", { name: "remove" }).length;
    fireEvent.click(screen.getByRole("button", { name: "add node" }));
    const after = screen.getAllByRole("button", { name: "remove" }).length;
    expect(after).toBe(before + 1);
  });

  it("removing a node reports a moved percentage", () => {
    render(<App />);
    fireEvent.click(screen.getAllByRole("button", { name: "remove" })[0]);
    expect(screen.getByText(/moved/)).toBeTruthy();
  });
});

describe("RateLimiterViz", () => {
  it("sending a burst updates accepted or rejected counts", () => {
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: "Rate Limiting" }));
    fireEvent.click(screen.getByRole("button", { name: "send burst of 10" }));
    // Both the accepted count and the total happen to read 10 here (default
    // token bucket capacity is 10, so a first burst of 10 is fully
    // accepted), which is itself a fine sanity check: assert there are at
    // least 2 such values rather than picking one arbitrarily.
    expect(screen.getAllByText("10").length).toBeGreaterThanOrEqual(2);
  });
});

describe("CircuitBreakerViz", () => {
  it("opens after enough consecutive failures and rejects the next call fast", () => {
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: "Circuit Breakers" }));
    const failButton = screen.getByRole("button", { name: "call fails" });
    fireEvent.click(failButton);
    fireEvent.click(failButton);
    fireEvent.click(failButton); // default threshold is 3

    expect(screen.getByText("open")).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "call succeeds" }));
    // The help text below the log also names this phrase on purpose, so at
    // least 2 matches is the correct expectation here, not exactly 1.
    expect(screen.getAllByText(/dependency was never called/).length).toBeGreaterThanOrEqual(2);
  });
});
