const { app, BrowserWindow, dialog, ipcMain, shell } = require("electron");
const { execFile, spawn } = require("node:child_process");
const { randomUUID } = require("node:crypto");
const path = require("node:path");

const repoRoot = path.resolve(__dirname, "..", "..", "..");
const activeRuns = new Map();

function createWindow() {
  const win = new BrowserWindow({
    width: 720,
    height: 640,
    minWidth: 560,
    minHeight: 520,
    title: "ComputeHop Control Center",
    backgroundColor: "#00000000",
    transparent: true,
    vibrancy: "under-window",
    visualEffectState: "active",
    titleBarStyle: "hiddenInset",
    trafficLightPosition: { x: 16, y: 18 },
    webPreferences: {
      preload: path.join(__dirname, "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false
    }
  });

  win.loadFile(path.join(__dirname, "index.html"));
}

app.whenReady().then(() => {
  createWindow();

  app.on("activate", () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      createWindow();
    }
  });
});

app.on("window-all-closed", () => {
  if (process.platform !== "darwin") {
    app.quit();
  }
});

ipcMain.handle("devices:list", async () => {
  const result = await runComputeHop(["devices"]);
  return {
    ok: result.ok,
    error: result.error,
    raw: result.stdout,
    devices: result.ok ? parseDevices(result.stdout) : []
  };
});

ipcMain.handle("app:openExternal", async (_event, target) => {
  if (typeof target !== "string") {
    return;
  }
  await shell.openExternal(target);
});

ipcMain.handle("project:choose", async () => {
  const result = await dialog.showOpenDialog({
    title: "Choose Project",
    properties: ["openDirectory"]
  });
  if (result.canceled || result.filePaths.length === 0) {
    return null;
  }
  return result.filePaths[0];
});

ipcMain.handle("jobs:start", async (event, request) => {
  const jobRequest = normalizeJobRequest(request);
  const argv = splitCommandLine(jobRequest.command);
  if (argv.length === 0) {
    throw new Error("Enter something to run.");
  }

  const args = ["run"];
  if (jobRequest.deviceID && jobRequest.deviceID !== "local") {
    args.push("--on", jobRequest.deviceID);
    if (jobRequest.workingDirectory) {
      args.push("-C", jobRequest.workingDirectory);
    } else {
      args.push("--no-project");
    }
  } else if (jobRequest.workingDirectory) {
    args.push("-C", jobRequest.workingDirectory);
  }
  args.push("--follow", ...argv);

  const cwd = jobRequest.workingDirectory || repoRoot;
  const runID = randomUUID();
  setImmediate(() => startComputeHopStream(event.sender, runID, args, {
    cwd,
    deviceSelector: jobRequest.deviceID === "local" ? "" : jobRequest.deviceID
  }));
  return { runID };
});

ipcMain.handle("jobs:stop", async (_event, runID) => {
  const record = activeRuns.get(String(runID));
  if (!record) {
    return { stopped: false };
  }

  if (record.jobID) {
    const args = ["cancel"];
    if (record.deviceSelector) {
      args.push("--on", record.deviceSelector);
    }
    args.push(record.jobID);
    const result = await runComputeHop(args, { cwd: repoRoot, timeout: 10000 });
    if (!result.ok) {
      sendRunEvent(record.webContents, String(runID), {
        type: "output",
        stream: "stderr",
        text: `\nCancel failed: ${result.error || "unknown error"}\n`
      });
    }
  }

  record.child.kill("SIGINT");
  return { stopped: true, cancelled: Boolean(record.jobID) };
});

async function runComputeHop(args, options) {
  const cwd = options?.cwd || process.cwd();
  const timeout = options?.timeout || 7000;
  const installed = await execCommand("computehop", args, cwd, timeout);
  if (installed.ok || !installed.missing) {
    return installed;
  }

  return execCommand("go", ["run", "./cmd/computehop", ...args], repoRoot, timeout);
}

function execCommand(command, args, cwd, timeout = 7000) {
  return new Promise((resolve) => {
    execFile(
      command,
      args,
      {
        cwd,
        timeout,
        maxBuffer: 8 * 1024 * 1024,
        windowsHide: true
      },
      (error, stdout, stderr) => {
        if (error) {
          resolve({
            ok: false,
            missing: error.code === "ENOENT",
            stdout: stdout || "",
            stderr: stderr || "",
            error: (stderr || error.message || "Command failed").trim()
          });
          return;
        }
        resolve({ ok: true, missing: false, stdout: stdout.trim(), stderr: stderr.trim(), error: "" });
      }
    );
  });
}

function startComputeHopStream(webContents, runID, args, options) {
  startProcessStream(webContents, runID, {
    command: "computehop",
    args,
    cwd: options.cwd,
    deviceSelector: options.deviceSelector,
    fallback: {
      command: "go",
      args: ["run", "./cmd/computehop", ...args],
      cwd: repoRoot,
      deviceSelector: options.deviceSelector
    }
  });
}

function startProcessStream(webContents, runID, spec) {
  let child;
  let spawnFailed = false;

  try {
    child = spawn(spec.command, spec.args, {
      cwd: spec.cwd,
      windowsHide: true
    });
  } catch (error) {
    sendRunEvent(webContents, runID, {
      type: "finished",
      ok: false,
      text: error.message || "Run failed."
    });
    return;
  }

  activeRuns.set(runID, {
    child,
    deviceSelector: spec.deviceSelector || "",
    jobID: null,
    webContents
  });
  sendRunEvent(webContents, runID, { type: "started" });

  child.stdout.on("data", (chunk) => {
    rememberSubmittedJobID(runID, chunk.toString());
    sendRunEvent(webContents, runID, {
      type: "output",
      stream: "stdout",
      text: chunk.toString()
    });
  });

  child.stderr.on("data", (chunk) => {
    rememberSubmittedJobID(runID, chunk.toString());
    sendRunEvent(webContents, runID, {
      type: "output",
      stream: "stderr",
      text: chunk.toString()
    });
  });

  child.once("error", (error) => {
    spawnFailed = true;
    activeRuns.delete(runID);
    if (error.code === "ENOENT" && spec.fallback) {
      startProcessStream(webContents, runID, spec.fallback);
      return;
    }
    sendRunEvent(webContents, runID, {
      type: "finished",
      ok: false,
      text: error.message || "Run failed."
    });
  });

  child.once("close", (code, signal) => {
    if (spawnFailed) {
      return;
    }
    activeRuns.delete(runID);
    const stopped = signal === "SIGINT" || signal === "SIGTERM";
    sendRunEvent(webContents, runID, {
      type: "finished",
      ok: code === 0,
      stopped,
      text: stopped
        ? "Stopped."
        : code === 0
          ? "Done."
          : `Exited with code ${code}.`
    });
  });
}

function rememberSubmittedJobID(runID, text) {
  const record = activeRuns.get(runID);
  if (!record || record.jobID) {
    return;
  }
  const match = text.match(/Submitted\s+([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/i);
  if (!match) {
    return;
  }
  record.jobID = match[1];
  sendRunEvent(record.webContents, runID, {
    type: "job",
    jobID: record.jobID
  });
}

function sendRunEvent(webContents, runID, payload) {
  if (webContents.isDestroyed()) {
    activeRuns.delete(runID);
    return;
  }
  webContents.send("jobs:event", { runID, ...payload });
}

function normalizeJobRequest(request) {
  if (!request || typeof request !== "object") {
    throw new Error("Job request is required.");
  }
  return {
    command: String(request.command || "").trim(),
    deviceID: String(request.deviceID || "local").trim(),
    workingDirectory: String(request.workingDirectory || "").trim()
  };
}

function splitCommandLine(input) {
  const tokens = [];
  let current = "";
  let quote = null;
  let escaping = false;

  for (const char of input) {
    if (escaping) {
      current += char;
      escaping = false;
      continue;
    }
    if (char === "\\") {
      escaping = true;
      continue;
    }
    if (quote) {
      if (char === quote) {
        quote = null;
      } else {
        current += char;
      }
      continue;
    }
    if (char === "'" || char === '"') {
      quote = char;
      continue;
    }
    if (/\s/.test(char)) {
      if (current !== "") {
        tokens.push(current);
        current = "";
      }
      continue;
    }
    current += char;
  }

  if (escaping) {
    throw new Error("Command ends with an unfinished escape.");
  }
  if (quote) {
    throw new Error("Command has an unfinished quote.");
  }
  if (current !== "") {
    tokens.push(current);
  }
  return tokens;
}

function parseDevices(stdout) {
  if (!stdout) {
    return [];
  }

  const lines = stdout
    .split(/\r?\n/)
    .map((line) => line.trimEnd())
    .filter(Boolean);

  if (lines.length <= 1) {
    return [];
  }

  return lines
    .slice(1)
    .map((line) => parseDeviceLine(line))
    .filter(Boolean);
}

function parseDeviceLine(line) {
  if (
    line === "Next:" ||
    line.startsWith("- ") ||
    line.startsWith("No connected or nearby devices.") ||
    line.startsWith("LAN discovery")
  ) {
    return null;
  }

  const columns = line.split(/\s{2,}/).map((column) => column.trim());
  if (columns.length >= 8) {
    return {
      name: columns[0],
      id: columns[1],
      connection: columns[2],
      role: columns[3],
      availability: columns[4],
      path: columns[5],
      address: columns[6],
      updated: columns[7]
    };
  }

  if (columns.length >= 6) {
    return {
      name: columns[0],
      id: columns[1],
      connection: columns[2],
      role: columns[3],
      availability: columns[2],
      path: "",
      address: columns[4],
      updated: columns[5]
    };
  }

  return null;
}
