"use strict";

const assert = require("node:assert/strict");
const { spawnSync } = require("node:child_process");
const test = require("node:test");

const { relaySignal, run } = require("../bin/fast-context.js");

function captureErrors() {
  const lines = [];
  return { lines, logError: (line) => lines.push(String(line)) };
}

test("rejects unsupported platforms", () => {
  const output = captureErrors();
  const result = run({ platform: "freebsd", arch: "x64", logError: output.logError });
  assert.equal(result.status, 1);
  assert.match(output.lines.join("\n"), /unsupported platform freebsd-x64/);
});

test("reports a missing optional platform package", () => {
  const output = captureErrors();
  const result = run({
    platform: "linux",
    arch: "x64",
    resolve: () => {
      throw new Error("missing");
    },
    logError: output.logError,
  });
  assert.equal(result.status, 1);
  assert.match(output.lines.join("\n"), /fast-context-linux-x64/);
});

test("injects bundled ripgrep and forwards args, stdio, and exit status", () => {
  let call;
  const result = run({
    platform: "linux",
    arch: "x64",
    args: ["unknown-command"],
    env: { FIXTURE: "true" },
    resolve: () => "/fixture/fast-context",
    loadRipgrep: () => ({ rgPath: "/fixture/rg" }),
    spawn: (binary, args, options) => {
      call = { binary, args, options };
      return { status: 2, signal: null };
    },
  });
  assert.equal(result.status, 2);
  assert.equal(call.binary, "/fixture/fast-context");
  assert.deepEqual(call.args, ["unknown-command"]);
  assert.equal(call.options.stdio, "inherit");
  assert.equal(call.options.env.FC_RG_PATH, "/fixture/rg");
  assert.equal(call.options.env.FIXTURE, "true");
});

test("preserves an explicit FC_RG_PATH", () => {
  let env;
  const result = run({
    platform: "linux",
    arch: "x64",
    env: { FC_RG_PATH: "/user/rg" },
    resolve: () => "/fixture/fast-context",
    loadRipgrep: () => ({ rgPath: "/bundled/rg" }),
    spawn: (_binary, _args, options) => {
      env = options.env;
      return { status: 0, signal: null };
    },
  });
  assert.equal(result.status, 0);
  assert.equal(env.FC_RG_PATH, "/user/rg");
});

test("reports ripgrep resolution and process launch failures", () => {
  const ripgrepOutput = captureErrors();
  const ripgrepResult = run({
    platform: "linux",
    arch: "x64",
    resolve: () => "/fixture/fast-context",
    loadRipgrep: () => {
      throw new Error("ripgrep missing");
    },
    logError: ripgrepOutput.logError,
  });
  assert.equal(ripgrepResult.status, 1);
  assert.match(ripgrepOutput.lines.join("\n"), /failed to resolve/);

  const launchOutput = captureErrors();
  const launchResult = run({
    platform: "linux",
    arch: "x64",
    resolve: () => "/fixture/fast-context",
    loadRipgrep: () => ({ rgPath: "/fixture/rg" }),
    spawn: () => ({ error: new Error("spawn failed"), status: null, signal: null }),
    logError: launchOutput.logError,
  });
  assert.equal(launchResult.status, 1);
  assert.match(launchOutput.lines.join("\n"), /failed to launch/);
});

test("returns the child signal for the entrypoint to relay", () => {
  const output = captureErrors();
  const result = run({
    platform: "linux",
    arch: "x64",
    resolve: () => "/fixture/fast-context",
    loadRipgrep: () => ({ rgPath: "/fixture/rg" }),
    spawn: () => ({ status: null, signal: "SIGTERM" }),
    logError: output.logError,
  });
  assert.equal(result.signal, "SIGTERM");
  assert.match(output.lines.join("\n"), /terminated by signal SIGTERM/);
});

test("relays the exact signal to the current process", () => {
  let call;
  const runtime = {
    pid: 42,
    kill: (pid, signal) => {
      call = { pid, signal };
    },
    exit: () => assert.fail("exit must not run when signal relay succeeds"),
  };
  relaySignal("SIGTERM", runtime);
  assert.deepEqual(call, { pid: 42, signal: "SIGTERM" });
});

test("real POSIX child observes the relayed signal", { skip: process.platform === "win32" }, () => {
  const launcher = require.resolve("../bin/fast-context.js");
  const script = `require(${JSON.stringify(launcher)}).relaySignal("SIGTERM"); setTimeout(() => process.exit(99), 1000);`;
  const result = spawnSync(process.execPath, ["-e", script]);
  assert.equal(result.status, null);
  assert.equal(result.signal, "SIGTERM");
});
