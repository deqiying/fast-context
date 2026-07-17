#!/usr/bin/env node
"use strict";

const { spawnSync } = require("node:child_process");

const targets = {
  "darwin-arm64": {
    packageName: "@deqiying/fast-context-darwin-arm64",
    binaryPath: "bin/fast-context",
  },
  "linux-x64": {
    packageName: "@deqiying/fast-context-linux-x64",
    binaryPath: "bin/fast-context",
  },
  "win32-x64": {
    packageName: "@deqiying/fast-context-win32-x64",
    binaryPath: "bin/fast-context.exe",
  },
};

function run(options = {}) {
  const platform = options.platform || process.platform;
  const arch = options.arch || process.arch;
  const args = options.args || process.argv.slice(2);
  const sourceEnv = options.env || process.env;
  const resolve = options.resolve || require.resolve;
  const loadRipgrep = options.loadRipgrep || (() => require("@vscode/ripgrep"));
  const spawn = options.spawn || spawnSync;
  const logError = options.logError || console.error;

  const targetKey = `${platform}-${arch}`;
  const target = targets[targetKey];
  if (!target) {
    logError(`fast-context: unsupported platform ${targetKey}`);
    logError(`Supported platforms: ${Object.keys(targets).join(", ")}`);
    return { status: 1, signal: null };
  }

  let binary;
  try {
    binary = resolve(`${target.packageName}/${target.binaryPath}`);
  } catch (_error) {
    logError(`fast-context: missing optional package ${target.packageName}`);
    logError("Reinstall fast-context with optional dependencies enabled.");
    return { status: 1, signal: null };
  }

  let rgPath;
  try {
    ({ rgPath } = loadRipgrep());
    if (typeof rgPath !== "string" || rgPath.length === 0) {
      throw new Error("rgPath is empty");
    }
  } catch (error) {
    logError("fast-context: failed to resolve @vscode/ripgrep");
    logError(error.message);
    return { status: 1, signal: null };
  }

  const env = { ...sourceEnv };
  if (!env.FC_RG_PATH) {
    env.FC_RG_PATH = rgPath;
  }

  const result = spawn(binary, args, { stdio: "inherit", env });
  if (result.error) {
    logError(`fast-context: failed to launch ${binary}`);
    logError(result.error.message);
    return { status: 1, signal: null };
  }
  if (result.signal) {
    logError(`fast-context: terminated by signal ${result.signal}`);
    return { status: 1, signal: result.signal };
  }
  return { status: result.status === null ? 1 : result.status, signal: null };
}

function relaySignal(signal, runtime = process, logError = console.error) {
  try {
    runtime.kill(runtime.pid, signal);
  } catch (error) {
    logError(`fast-context: failed to relay signal ${signal}`);
    logError(error.message);
    runtime.exit(1);
  }
}

if (require.main === module) {
  const result = run();
  if (result.signal) {
    relaySignal(result.signal);
  } else {
    process.exit(result.status);
  }
}

module.exports = { relaySignal, run, targets };
