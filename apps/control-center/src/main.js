const { app, BrowserWindow, dialog, ipcMain, shell } = require("electron");
const { execFile } = require("node:child_process");
const path = require("node:path");

const repoRoot = path.resolve(__dirname, "..", "..", "..");

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

ipcMain.handle("jobs:run", async (_event, request) => {
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
  const result = await runComputeHop(args, { cwd, timeout: 30 * 60 * 1000 });
  return {
    ok: result.ok,
    output: [result.stdout, result.stderr].filter(Boolean).join("\n").trim(),
    error: result.error
  };
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
